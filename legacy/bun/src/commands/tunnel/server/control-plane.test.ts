import { describe, expect, test } from 'bun:test'
import { TunnelError } from '../types'
import { normalizeCustomDomains, normalizeExactHostname, normalizeHttpLocation, TunnelControlPlane } from './control-plane'
import { TunnelDatabase } from './database'

function fixture(range = { start: 20000, end: 20002 }): { database: TunnelDatabase, controlPlane: TunnelControlPlane, ownerId: string } {
  const database = new TunnelDatabase(':memory:')
  const ownerId = 'test-owner'
  database.sqlite.query(`
    INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
    VALUES(?, 'environment', 'admin', 'admin', 'admin', NULL, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')
  `).run(ownerId)
  return { database, controlPlane: new TunnelControlPlane(database, range), ownerId }
}

describe('TunnelControlPlane', () => {
  test('normalizes exact internationalized hostnames and rejects non-hostnames', () => {
    expect(normalizeExactHostname('Example.COM.')).toBe('example.com')
    expect(normalizeExactHostname('例子.测试')).toBe('xn--fsqu00a.xn--0zwm56d')
    for (const invalid of ['https://example.com', '*.example.com', 'example.com/path', '127.0.0.1', 'localhost', 'example.com:80'])
      expect(() => normalizeExactHostname(invalid)).toThrow(TunnelError)
  })

  test('normalizes one HTTP location and rejects values FRP cannot route safely', () => {
    expect(normalizeHttpLocation(undefined)).toBeNull()
    expect(normalizeHttpLocation(null)).toBeNull()
    expect(normalizeHttpLocation(' /service-a ')).toBe('/service-a')
    for (const invalid of ['service-a', '/with space', '/path?query=1', '/path#fragment', ''])
      expect(() => normalizeHttpLocation(invalid)).toThrow(TunnelError)
  })

  test('normalizes and de-duplicates HTTP custom domains', () => {
    expect(normalizeCustomDomains(['App.Example.com', 'app.example.com', '例子.测试'])).toEqual(['app.example.com', 'xn--fsqu00a.xn--0zwm56d'])
    expect(normalizeCustomDomains(undefined, 'Legacy.Example.com')).toEqual(['legacy.example.com'])
    expect(() => normalizeCustomDomains([])).toThrow('at least one custom domain')
  })

  test('keeps Client Tokens recoverable and preserves tunnels across rotation', () => {
    const { database, controlPlane, ownerId } = fixture()
    const client = controlPlane.createClient(ownerId, '  Office Mac  ')
    expect(client.remark).toBe('Office Mac')
    expect(controlPlane.createClient(ownerId).remark).toBe('')
    expect(controlPlane.createClient(ownerId, '   ').remark).toBe('')
    expect(controlPlane.createClient(ownerId, '  line one\nline two  ').remark).toBe('line one\nline two')
    expect(() => controlPlane.createClient(ownerId, 'x'.repeat(101))).toThrow('Client Remark')
    expect(controlPlane.updateClientRemark(client.id, 'Office\ngateway').remark).toBe('Office\ngateway')
    expect(controlPlane.updateClientRemark(client.id, '').remark).toBe('')
    expect(controlPlane.getClient(client.id).token).toBe(client.token)
    const tunnel = controlPlane.createTunnel(client.id, { protocol: 'http', hostname: 'app.example.com', localPort: 3000 })
    const rotated = controlPlane.rotateClientToken(client.id)
    expect(rotated.token).not.toBe(client.token)
    expect(rotated.revocationPending).toBe(true)
    expect(controlPlane.findClientByToken(client.token)).toBeUndefined()
    expect(controlPlane.listTunnels(client.id)).toEqual([tunnel])
    controlPlane.acknowledgeReplacementToken(client.id)
    expect(controlPlane.getClient(client.id).revocationPending).toBe(false)
    database.close()
  })

  test('stores one independently enabled HTTP location per Tunnel Definition', () => {
    const { database, controlPlane, ownerId } = fixture()
    const first = controlPlane.createClient(ownerId, 'First client')
    const second = controlPlane.createClient(ownerId, 'Second client')
    const tunnel = controlPlane.createTunnel(first.id, { protocol: 'http', customDomains: ['APP.example.com', 'alias.example.com'], location: '/service-a', localPort: 3000, enabled: false })
    if (tunnel.protocol !== 'http')
      throw new Error('Expected an HTTP Tunnel Definition')
    expect(tunnel).toMatchObject({ customDomains: ['app.example.com', 'alias.example.com'], location: '/service-a', enabled: false })
    expect(() => controlPlane.createTunnel(second.id, { protocol: 'http', customDomains: ['alias.example.com'], location: '/service-a', localPort: 3001 })).toThrow('custom domain and location are already reserved')
    const sibling = controlPlane.createTunnel(second.id, { protocol: 'http', customDomains: ['app.example.com'], location: '/service-b', localPort: 3002 })
    if (sibling.protocol !== 'http')
      throw new Error('Expected an HTTP Tunnel Definition')
    expect(sibling.location).toBe('/service-b')
    const catchAll = controlPlane.createTunnel(second.id, { protocol: 'http', customDomains: ['app.example.com'], localPort: 3003 })
    expect(catchAll.protocol === 'http' && catchAll.location).toBeNull()
    controlPlane.updateTunnel(sibling.id, { enabled: false })
    expect(controlPlane.getTunnel(tunnel.id).enabled).toBe(false)
    expect(controlPlane.getTunnel(sibling.id).enabled).toBe(false)
    controlPlane.deleteTunnel(tunnel.id)
    const replacement = controlPlane.createTunnel(second.id, { protocol: 'http', customDomains: ['app.example.com'], location: '/service-a', localPort: 3001 })
    expect(replacement.protocol === 'http' && replacement.customDomains).toEqual(['app.example.com'])
    database.close()
  })

  test('rolls back an HTTP route edit when its new reservation conflicts', () => {
    const { database, controlPlane, ownerId } = fixture()
    const first = controlPlane.createClient(ownerId, 'First client')
    const second = controlPlane.createClient(ownerId, 'Second client')
    const firstTunnel = controlPlane.createTunnel(first.id, { protocol: 'http', customDomains: ['app.example.com'], location: '/first', localPort: 3000 })
    const secondTunnel = controlPlane.createTunnel(second.id, { protocol: 'http', customDomains: ['app.example.com'], location: '/second', localPort: 3001 })

    expect(() => controlPlane.updateTunnel(secondTunnel.id, { location: '/first' })).toThrow('already reserved')
    const unchanged = controlPlane.getTunnel(secondTunnel.id)
    expect(unchanged.protocol === 'http' && unchanged.location).toBe('/second')
    controlPlane.updateTunnel(firstTunnel.id, { location: '/moved' })
    const moved = controlPlane.updateTunnel(secondTunnel.id, { location: '/first' })
    expect(moved.protocol === 'http' && moved.location).toBe('/first')
    database.close()
  })

  test('normalizes typed FRP options and preserves write-only Basic Auth passwords on patch', () => {
    const { database, controlPlane, ownerId } = fixture()
    const client = controlPlane.createClient(ownerId, 'Advanced HTTP')
    const tunnel = controlPlane.createTunnel(client.id, {
      label: ' Ticket H5 ',
      protocol: 'http',
      customDomains: ['routes.example.com'],
      location: '/service-a',
      localPort: 9001,
      options: {
        transport: {
          useEncryption: true,
          useCompression: true,
          bandwidthLimit: { value: 2, unit: 'MB', mode: 'server' },
          proxyProtocolVersion: 'v2',
        },
        healthCheck: {
          type: 'http',
          path: '/health',
          intervalSeconds: 10,
          timeoutSeconds: 3,
          maxFailed: 2,
          headers: [{ name: 'X-Probe', value: 'ycy' }],
        },
        http: {
          basicAuth: { username: 'operator', password: 'secret-value' },
          hostHeaderRewrite: 'internal.example.com',
          requestHeaders: [{ name: 'X-Forwarded-By', value: 'ycy' }],
          responseHeaders: [{ name: 'X-Tunnel', value: 'ticket' }],
        },
      },
    })

    expect(tunnel.label).toBe('Ticket H5')
    expect(tunnel.options).toEqual({
      transport: {
        useEncryption: true,
        useCompression: true,
        bandwidthLimit: { value: 2, unit: 'MB', mode: 'server' },
        proxyProtocolVersion: 'v2',
      },
      healthCheck: {
        type: 'http',
        path: '/health',
        intervalSeconds: 10,
        timeoutSeconds: 3,
        maxFailed: 2,
        headers: [{ name: 'X-Probe', value: 'ycy' }],
      },
      http: {
        basicAuth: { username: 'operator', password: 'secret-value' },
        hostHeaderRewrite: 'internal.example.com',
        requestHeaders: [{ name: 'X-Forwarded-By', value: 'ycy' }],
        responseHeaders: [{ name: 'X-Tunnel', value: 'ticket' }],
      },
    })

    const changed = controlPlane.updateTunnel(tunnel.id, { options: { http: { basicAuth: { username: 'renamed' } } } })
    expect(changed.options.http?.basicAuth).toEqual({ username: 'renamed', password: 'secret-value' })
    expect(controlPlane.updateTunnel(tunnel.id, { options: { http: { basicAuth: null } } }).options.http?.basicAuth).toBeNull()
    database.close()
  })

  test('allocates the lowest free port independently per transport protocol', () => {
    const { database, controlPlane, ownerId } = fixture()
    const first = controlPlane.createClient(ownerId, 'First client')
    const second = controlPlane.createClient(ownerId, 'Second client')
    const tcp = controlPlane.createTunnel(first.id, { protocol: 'tcp', localPort: 5432 })
    const udp = controlPlane.createTunnel(second.id, { protocol: 'udp', localPort: 53 })
    const nextTcp = controlPlane.createTunnel(second.id, { protocol: 'tcp', localPort: 5433 })
    expect(tcp.serverPort).toBe(20000)
    expect(udp.serverPort).toBe(20000)
    expect(nextTcp.serverPort).toBe(20001)
    expect(() => controlPlane.createTunnel(first.id, { protocol: 'tcp', serverPort: 20001, localPort: 1 })).toThrow('already reserved')
    database.close()
  })

  test('imports disabled tunnels atomically with one desired revision and event', () => {
    const { database, controlPlane, ownerId } = fixture()
    const client = controlPlane.createClient(ownerId, 'Import target')
    const events: unknown[] = []
    controlPlane.subscribe(event => events.push(event))

    const imported = controlPlane.importTunnels(client.id, [
      { protocol: 'http', customDomains: ['import.example.com'], location: '/app', localPort: 3000, enabled: true },
      { protocol: 'tcp', serverPort: 20001, localPort: 5432, enabled: true },
    ])
    expect(imported).toHaveLength(2)
    expect(imported.every(tunnel => !tunnel.enabled)).toBe(true)
    expect(controlPlane.getClient(client.id).desiredRevision).toBe(1)
    expect(events).toEqual([{ type: 'desired_state', clientId: client.id, ownerAccountId: ownerId }])

    expect(() => controlPlane.importTunnels(client.id, [
      { protocol: 'http', customDomains: ['import.example.com'], location: '/app', localPort: 3001 },
      { protocol: 'udp', serverPort: 20002, localPort: 53 },
    ])).toThrow('already reserved')
    expect(controlPlane.listTunnels(client.id)).toHaveLength(2)
    expect(controlPlane.getClient(client.id).desiredRevision).toBe(1)
    database.close()
  })

  test('increments Desired Revision atomically for every mutation and bounds Applied Revision', () => {
    const { database, controlPlane, ownerId } = fixture()
    const client = controlPlane.createClient(ownerId, 'DNS client')
    const tunnel = controlPlane.createTunnel(client.id, { protocol: 'udp', localPort: 53 })
    expect(controlPlane.getClient(client.id).desiredRevision).toBe(1)
    controlPlane.updateTunnel(tunnel.id, { enabled: false })
    expect(controlPlane.getClient(client.id).desiredRevision).toBe(2)
    controlPlane.recordAppliedRevision(client.id, 1)
    expect(controlPlane.getClient(client.id).lastAppliedRevision).toBe(1)
    expect(() => controlPlane.recordAppliedRevision(client.id, 3)).toThrow('cannot exceed')
    controlPlane.deleteTunnel(tunnel.id)
    expect(controlPlane.getClient(client.id).desiredRevision).toBe(3)
    database.close()
  })

  test('cascade deletion frees reservations and removes the client', () => {
    const { database, controlPlane, ownerId } = fixture()
    const first = controlPlane.createClient(ownerId, 'First client')
    controlPlane.createTunnel(first.id, { protocol: 'tcp', serverPort: 20002, localPort: 22 })
    controlPlane.deleteClient(first.id)
    expect(() => controlPlane.getClient(first.id)).toThrow('not found')
    const second = controlPlane.createClient(ownerId, 'Second client')
    expect(controlPlane.createTunnel(second.id, { protocol: 'tcp', serverPort: 20002, localPort: 22 }).serverPort).toBe(20002)
    database.close()
  })
})
