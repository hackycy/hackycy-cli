import type { FrpChild } from '../frp/supervisor'
import type { FrpProcessState, ServerTunnelConfig } from '../types'
import type { FrpsAvailability } from './agent-gateway'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { resolveFrpArtifact } from '../frp/manifest'
import { FrpSupervisor } from '../frp/supervisor'
import { TUNNEL_PROTOCOL_VERSION } from '../types'
import { AgentGateway } from './agent-gateway'
import { TunnelControlPlane } from './control-plane'
import { TunnelDatabase } from './database'
import { FrpsConfiguration } from './frps-configuration'
import { startTunnelHttpServer } from './http'
import { TunnelManagement } from './tunnel-management'

class FakeChild implements FrpChild {
  readonly pid = 42
  readonly exited: Promise<number>
  private exit!: (code: number) => void

  constructor() {
    this.exited = new Promise(resolve => this.exit = resolve)
  }

  kill(): void {
    this.exit(0)
  }

  crash(code = 1): void {
    this.exit(code)
  }
}

class FakeFrpsAvailability implements FrpsAvailability {
  private readonly listeners = new Set<(state: { state: FrpProcessState }) => void>()

  constructor(private current: FrpProcessState = 'running') {}

  state(): { state: FrpProcessState } {
    return { state: this.current }
  }

  observe(listener: (state: { state: FrpProcessState }) => void): () => void {
    this.listeners.add(listener)
    listener(this.state())
    return () => this.listeners.delete(listener)
  }

  set(state: FrpProcessState): void {
    this.current = state
    for (const listener of this.listeners)
      listener(this.state())
  }
}

interface Fixture {
  database: TunnelDatabase
  controlPlane: TunnelControlPlane
  gateway: AgentGateway
  frps: FrpSupervisor
  crashNextFrps: () => void
  management: TunnelManagement
  server: ReturnType<typeof startTunnelHttpServer>
  dataDir: string
}

const fixtures: Fixture[] = []

afterEach(async () => {
  for (const fixture of fixtures.splice(0)) {
    fixture.management.stop()
    fixture.gateway.stop()
    await fixture.server.stop()
    await fixture.frps.stop()
    fixture.database.close()
    await rm(fixture.dataDir, { recursive: true, force: true })
  }
})

async function fixture(frpToken?: string, availability?: FrpsAvailability): Promise<Fixture> {
  const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-http-'))
  const database = new TunnelDatabase(':memory:')
  const controlPlane = new TunnelControlPlane(database, { start: 20000, end: 20002 })
  const gateway = new AgentGateway(controlPlane, 7000, frpToken ?? controlPlane.internalFrpToken(), undefined, availability)
  let crashNextFrps = false
  const frps = new FrpSupervisor({
    binaryPath: '/frps',
    role: 'frps',
    activationGraceMs: 10,
    spawn: () => {
      const child = new FakeChild()
      if (crashNextFrps)
        queueMicrotask(() => child.crash())
      crashNextFrps = false
      return child
    },
  })
  const config: ServerTunnelConfig = {
    address: '127.0.0.1',
    controlPort: 0,
    frpPort: 7000,
    httpPort: 8080,
    portRange: { start: 20000, end: 20002 },
    ...(frpToken ? { frpToken } : {}),
    dataDir,
    adminUser: 'admin',
    adminPassword: 'admin-secret',
  }
  const management = await TunnelManagement.create({ database, controlPlane, gateway, frps, frpsConfiguration: new FrpsConfiguration(config), serverConfig: config })
  const server = startTunnelHttpServer({ management, gateway, address: config.address, controlPort: config.controlPort })
  const result = { database, controlPlane, gateway, frps, crashNextFrps: () => crashNextFrps = true, management, server, dataDir }
  fixtures.push(result)
  return result
}

async function login(server: Fixture['server'], username = 'admin', password = 'admin-secret'): Promise<string> {
  const response = await fetch(new URL('/api/session', server.url), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  expect(response.status).toBe(200)
  return response.headers.get('set-cookie')!.split(';')[0]!
}

function request(server: Fixture['server'], pathname: string, cookie: string, init: RequestInit = {}): Promise<Response> {
  return fetch(new URL(pathname, server.url), {
    ...init,
    headers: { Cookie: cookie, Origin: server.url.origin, ...init.headers },
  })
}

function openAgent(server: Fixture['server'], token: string): Promise<{ socket: WebSocket, firstMessage: Promise<any> }> {
  const url = new URL('/api/agent', server.url)
  url.protocol = 'ws:'
  const BunWebSocket = WebSocket as unknown as { new (url: string | URL, options: Bun.WebSocketOptions): WebSocket }
  const socket = new BunWebSocket(url, { headers: { Authorization: `Bearer ${token}` } })
  const firstMessage = new Promise<any>((resolve, reject) => {
    socket.addEventListener('error', reject, { once: true })
    socket.addEventListener('open', () => socket.send(JSON.stringify({
      type: 'hello',
      tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
      ycyVersion: 'test',
      platform: process.platform,
      architecture: process.arch,
      lastAppliedRevision: 0,
    })), { once: true })
    socket.addEventListener('message', event => resolve(JSON.parse(String(event.data))), { once: true })
  })
  return Promise.resolve({ socket, firstMessage })
}

describe('Tunnel HTTP control plane', () => {
  test('bounds account sessions with least-recently-used eviction', async () => {
    const value = await fixture()
    const cookies: string[] = []
    for (let index = 0; index < 8; index++)
      cookies.push(await login(value.server))
    expect((await request(value.server, '/api/state', cookies[0]!)).status).toBe(200)
    cookies.push(await login(value.server))

    expect((await request(value.server, '/api/state', cookies[1]!)).status).toBe(401)
    expect((await request(value.server, '/api/state', cookies[0]!)).status).toBe(200)
    expect((await request(value.server, '/api/state', cookies.at(-1)!)).status).toBe(200)
  })

  test('returns and refreshes the authenticated session cookie on management requests', async () => {
    const value = await fixture()
    const cookie = await login(value.server)

    const session = await request(value.server, '/api/session', cookie)
    expect(session.status).toBe(200)
    expect(await session.json()).toMatchObject({ version: 1, authenticated: true, account: { username: 'admin' } })
    expect(session.headers.get('set-cookie')).toContain('Max-Age=604800')

    const state = await request(value.server, '/api/state', cookie)
    expect(state.status).toBe(200)
    expect(state.headers.get('set-cookie')).toContain('Max-Age=604800')
  })

  test('returns a storage error instead of renewing an undurable authenticated session', async () => {
    const value = await fixture()
    const cookie = await login(value.server)
    const directory = path.join(value.dataDir, 'sessions')
    await rm(directory, { recursive: true, force: true })
    await writeFile(directory, 'unavailable')

    const response = await request(value.server, '/api/state', cookie)

    expect(response.status).toBe(503)
    expect(await response.json()).toEqual({
      version: 1,
      error: { code: 'SESSION_UNAVAILABLE', message: 'Session storage is unavailable' },
    })
  })

  test('protects admin routes and performs client and tunnel mutations', async () => {
    const value = await fixture()
    expect((await fetch(new URL('/healthz', value.server.url))).status).toBe(200)
    expect((await fetch(new URL('/api/state', value.server.url))).status).toBe(401)
    const rejected = await fetch(new URL('/api/session', value.server.url), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'wrong' }),
    })
    expect(rejected.status).toBe(401)
    const cookie = await login(value.server)
    const createdResponse = await request(value.server, '/api/clients', cookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    })
    const client = (await createdResponse.json()).client
    expect(createdResponse.status).toBe(201)
    expect(client.remark).toBe('')
    expect(client.token).toStartWith('ycy_')

    const updatedResponse = await request(value.server, `/api/clients/${client.id}`, cookie, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ remark: 'Home lab\nGateway' }),
    })
    expect(updatedResponse.status).toBe(200)
    expect((await updatedResponse.json()).client).toMatchObject({ remark: 'Home lab\nGateway', token: client.token })

    const tunnelResponse = await request(value.server, `/api/clients/${client.id}/tunnels`, cookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ protocol: 'http', customDomains: ['App.Example.com', 'Alias.Example.com'], location: '/service-a', localPort: 3000 }),
    })
    const tunnel = (await tunnelResponse.json()).tunnel
    expect(tunnelResponse.status).toBe(201)
    expect(tunnel.customDomains).toEqual(['app.example.com', 'alias.example.com'])
    expect(tunnel.location).toBe('/service-a')
    const detail = await request(value.server, `/api/clients/${client.id}`, cookie).then(response => response.json())
    expect(detail.client.desiredRevision).toBe(1)
    expect(detail.tunnels[0].state).toBe('Pending')
    const state = await request(value.server, '/api/state', cookie).then(response => response.json())
    expect(state.counts).toMatchObject({ clients: 1, tunnels: 1, pending: 1 })

    const patchedTunnel = await request(value.server, `/api/tunnels/${tunnel.id}`, cookie, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: false }),
    })
    expect(patchedTunnel.status).toBe(200)
    expect((await patchedTunnel.json()).tunnel.enabled).toBe(false)
    expect((await request(value.server, `/api/tunnels/${tunnel.id}`, cookie, { method: 'DELETE' })).status).toBe(204)
    expect((await request(value.server, `/api/clients/${client.id}`, cookie).then(response => response.json())).tunnels).toEqual([])
    expect((await request(value.server, `/api/clients/${client.id}`, cookie, { method: 'DELETE' })).status).toBe(204)
    expect((await request(value.server, '/api/clients', cookie).then(response => response.json())).clients).toEqual([])
    expect((await request(value.server, '/api/session', cookie, { method: 'DELETE' })).status).toBe(204)
    expect((await request(value.server, '/api/state', cookie)).status).toBe(401)
  })

  test('keeps HTTP Basic Auth recoverable for agents without returning its password to browsers', async () => {
    const value = await fixture()
    const cookie = await login(value.server)
    const client = value.controlPlane.createClient('environment-admin', 'Credential fixture')
    const created = await request(value.server, `/api/clients/${client.id}/tunnels`, cookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        label: 'Protected app',
        protocol: 'http',
        customDomains: ['protected.example.com'],
        location: '/app',
        localPort: 3000,
        options: { http: { basicAuth: { username: 'operator', password: 'secret-value' } } },
      }),
    })
    expect(created.status).toBe(201)
    const publicTunnel = (await created.json()).tunnel
    expect(publicTunnel.options.http.basicAuth).toEqual({ username: 'operator', passwordConfigured: true })
    expect(JSON.stringify(publicTunnel)).not.toContain('secret-value')

    const detail = await request(value.server, `/api/clients/${client.id}`, cookie).then(response => response.json())
    expect(detail.tunnels[0].options.http.basicAuth).toEqual({ username: 'operator', passwordConfigured: true })
    expect(JSON.stringify(detail)).not.toContain('secret-value')
    expect(value.controlPlane.snapshot(client.id).tunnels.find(tunnel => tunnel.protocol === 'http')?.options.http?.basicAuth).toEqual({ username: 'operator', password: 'secret-value' })
  })

  test('previews and imports selected TOML tunnels disabled without exposing credentials', async () => {
    const value = await fixture()
    const cookie = await login(value.server)
    const client = value.controlPlane.createClient('environment-admin', 'Import fixture')
    const source = `
serverAddr = "tunnel.example.com"

[[proxies]]
name = "app"
type = "http"
localPort = 3000
customDomains = ["import.example.com"]
locations = ["/app", "/admin"]
httpUser = "operator"
httpPassword = "secret-value"

[[proxies]]
name = "database"
type = "tcp"
localPort = 5432
remotePort = 20001
`
    const previewResponse = await request(value.server, `/api/clients/${client.id}/tunnels/import/preview`, cookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source }),
    })
    expect(previewResponse.status).toBe(200)
    const preview = await previewResponse.json()
    expect(preview.candidates).toHaveLength(3)
    expect(preview.candidates.every((candidate: any) => candidate.basicAuth?.password === undefined)).toBe(true)
    expect(JSON.stringify(preview)).not.toContain('secret-value')

    const importedResponse = await request(value.server, `/api/clients/${client.id}/tunnels/import`, cookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, candidateIds: preview.candidates.map((candidate: any) => candidate.id) }),
    })
    expect(importedResponse.status).toBe(201)
    const imported = await importedResponse.json()
    expect(imported.tunnels).toHaveLength(3)
    expect(imported.tunnels.every((tunnel: any) => tunnel.enabled === false)).toBe(true)
    expect(JSON.stringify(imported)).not.toContain('secret-value')
    expect(value.controlPlane.getClient(client.id).desiredRevision).toBe(1)
    expect(value.controlPlane.snapshot(client.id).tunnels.find(tunnel => tunnel.protocol === 'http')?.options.http?.basicAuth).toEqual({ username: 'operator', password: 'secret-value' })

    const conflict = await request(value.server, `/api/clients/${client.id}/tunnels/import`, cookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, candidateIds: [preview.candidates[0].id] }),
    })
    expect(conflict.status).toBe(409)
    expect(value.controlPlane.listTunnels(client.id)).toHaveLength(3)
    expect(value.controlPlane.getClient(client.id).desiredRevision).toBe(1)
  })

  test('authenticates one agent, pushes snapshots, records acknowledgements, and revokes rotation', async () => {
    const value = await fixture()
    const client = value.controlPlane.createClient('environment-admin', 'Agent fixture')
    const { socket, firstMessage } = await openAgent(value.server, client.token)
    const welcome = await firstMessage
    const artifact = resolveFrpArtifact()
    expect(welcome).toMatchObject({
      type: 'welcome',
      requiredFrpVersion: artifact.version,
      snapshot: { revision: 0, tunnels: [] },
    })
    expect(typeof welcome.internalFrpToken).toBe('string')
    const duplicate = value.gateway.authorize(new Request(new URL('/api/agent', value.server.url), { headers: { Authorization: `Bearer ${client.token}` } }))
    expect(duplicate).toBeInstanceOf(Response)
    expect((duplicate as Response).status).toBe(409)

    const desiredMessage = new Promise<any>(resolve => socket.addEventListener('message', event => resolve(JSON.parse(String(event.data))), { once: true }))
    value.controlPlane.createTunnel(client.id, { protocol: 'tcp', localPort: 22 })
    const desired = await desiredMessage
    expect(desired).toMatchObject({ type: 'desired_state', snapshot: { revision: 1 } })
    socket.send(JSON.stringify({ type: 'apply_result', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, revision: 1, success: true }))
    for (let attempt = 0; attempt < 50 && value.controlPlane.getClient(client.id).lastAppliedRevision !== 1; attempt++)
      await Bun.sleep(2)
    expect(value.controlPlane.getClient(client.id).lastAppliedRevision).toBe(1)

    const cookie = await login(value.server)
    const restartMessage = new Promise<any>(resolve => socket.addEventListener('message', event => resolve(JSON.parse(String(event.data))), { once: true }))
    const restarted = await request(value.server, `/api/clients/${client.id}/restart`, cookie, { method: 'POST' })
    expect(restarted.status).toBe(202)
    expect(await restartMessage).toMatchObject({ type: 'restart_frpc' })

    const revokeMessage = new Promise<any>(resolve => socket.addEventListener('message', event => resolve(JSON.parse(String(event.data))), { once: true }))
    const rotated = value.controlPlane.rotateClientToken(client.id)
    expect(await revokeMessage).toMatchObject({ type: 'revoke', reason: 'rotated' })
    if (socket.readyState !== WebSocket.CLOSED)
      await new Promise<void>(resolve => socket.addEventListener('close', () => resolve(), { once: true }))

    const rejectedProbe = await fetch(new URL('/api/agent', value.server.url), { headers: { Authorization: `Bearer ${client.token}` } })
    expect(rejectedProbe.status).toBe(401)
    const acceptedProbe = await fetch(new URL('/api/agent', value.server.url), { headers: { Authorization: `Bearer ${rotated.token}` } })
    expect(acceptedProbe.status).toBe(426)
  })

  test('shares a configured FRP token with trusted agents without exposing it to the management API', async () => {
    const value = await fixture('external-frp-token')
    const client = value.controlPlane.createClient('environment-admin', 'Configured FRP token')
    const { socket, firstMessage } = await openAgent(value.server, client.token)

    expect((await firstMessage).internalFrpToken).toBe('external-frp-token')
    const cookie = await login(value.server)
    const state = await request(value.server, '/api/state', cookie).then(response => response.json())
    expect(JSON.stringify(state)).not.toContain('external-frp-token')
    socket.close()
  })

  test('gates agent handshakes on frps availability and closes sessions when frps stops', async () => {
    const availability = new FakeFrpsAvailability('stopped')
    const value = await fixture(undefined, availability)
    const client = value.controlPlane.createClient('environment-admin', 'FRPS availability')

    const unavailable = await fetch(new URL('/api/agent', value.server.url), { headers: { Authorization: `Bearer ${client.token}` } })
    expect(unavailable.status).toBe(503)
    expect(await unavailable.json()).toMatchObject({ error: { code: 'FRPS_UNAVAILABLE' } })

    availability.set('running')
    const { socket, firstMessage } = await openAgent(value.server, client.token)
    expect(await firstMessage).toMatchObject({ type: 'welcome' })
    const closed = new Promise<void>(resolve => socket.addEventListener('close', () => resolve(), { once: true }))
    availability.set('recovering')
    await closed

    const rejectedAfterStop = await fetch(new URL('/api/agent', value.server.url), { headers: { Authorization: `Bearer ${client.token}` } })
    expect(rejectedAfterStop.status).toBe(503)
    availability.set('running')
    const reconnected = await openAgent(value.server, client.token)
    expect(await reconnected.firstMessage).toMatchObject({ type: 'welcome' })
    reconnected.socket.close()
  })

  test('keeps a failed Desired Revision in Error while the previous child is running', async () => {
    const value = await fixture()
    const cookie = await login(value.server)
    const client = value.controlPlane.createClient('environment-admin', 'Failure fixture')
    const { socket, firstMessage } = await openAgent(value.server, client.token)
    await firstMessage

    const desiredMessage = new Promise<void>(resolve => socket.addEventListener('message', () => resolve(), { once: true }))
    value.controlPlane.createTunnel(client.id, { protocol: 'http', hostname: 'failure.example.com', localPort: 3000 })
    await desiredMessage
    socket.send(JSON.stringify({
      type: 'apply_result',
      tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
      revision: 1,
      success: false,
      error: { code: 'ACTIVATION_FAILED', message: 'candidate exited', revision: 1 },
    }))
    socket.send(JSON.stringify({ type: 'process_state', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, state: 'running' }))
    for (let attempt = 0; attempt < 50 && value.gateway.state(client.id).lastError?.revision !== 1; attempt++)
      await Bun.sleep(2)

    expect(value.gateway.state(client.id)).toMatchObject({ processState: 'running', lastError: { revision: 1 } })
    const detail = await request(value.server, `/api/clients/${client.id}`, cookie).then(response => response.json())
    expect(detail.tunnels[0].state).toBe('Error')
    socket.close()
  })

  test('controls the supervised frps process through every server action', async () => {
    const value = await fixture()
    const cookie = await login(value.server)

    for (const action of ['start', 'restart', 'stop'] as const) {
      const response = await request(value.server, `/api/server/frp/${action}`, cookie, { method: 'POST' })
      expect(response.status).toBe(200)
      expect((await response.json()).server.frps.state).toBe(action === 'stop' ? 'stopped' : 'running')
    }
  })

  test('lets administrators update the managed custom 404 page without exposing its path', async () => {
    const value = await fixture()
    const endpoint = '/api/server/frps/config/custom-404-page'
    expect((await fetch(new URL(endpoint, value.server.url))).status).toBe(401)

    const cookie = await login(value.server)
    const empty = await request(value.server, endpoint, cookie)
    expect(empty.status).toBe(200)
    expect(await empty.json()).toEqual({ version: 1, content: '' })

    const saved = await request(value.server, endpoint, cookie, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: '<main>Configured 404</main>' }),
    })
    expect(saved.status).toBe(200)
    expect(await saved.json()).toEqual({ version: 1, content: '<main>Configured 404</main>' })
    expect(await Bun.file(path.join(value.dataDir, '404.html')).text()).toBe('<main>Configured 404</main>')
    expect((await request(value.server, endpoint, cookie, { method: 'POST' })).status).toBe(405)

    const restored = await request(value.server, endpoint, cookie, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: '' }),
    })
    expect(restored.status).toBe(200)
    expect(await Bun.file(path.join(value.dataDir, '404.html')).exists()).toBe(false)

    const tooLarge = await request(value.server, endpoint, cookie, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: 'x'.repeat(512 * 1024 + 1) }),
    })
    expect(tooLarge.status).toBe(400)
    expect(await tooLarge.json()).toMatchObject({ error: { code: 'INVALID_CUSTOM_404_PAGE' } })
  })

  test('reports failed frps restarts without claiming that frps is running', async () => {
    const value = await fixture()
    const cookie = await login(value.server)
    expect((await request(value.server, '/api/server/frp/start', cookie, { method: 'POST' })).status).toBe(200)
    value.crashNextFrps()

    const restarted = await request(value.server, '/api/server/frp/restart', cookie, { method: 'POST' })
    expect(restarted.status).toBe(500)
    const failure = await restarted.json()
    expect(failure).toMatchObject({
      error: {
        code: 'ACTIVATION_FAILED',
        message: expect.stringMatching(/FRP bind 127\.0\.0\.1:7000.*HTTP vhost 127\.0\.0\.1:8080.*frps exited with code 1 during startup.*lsof -nP -iTCP:7000 -sTCP:LISTEN/s),
      },
    })
    expect((await request(value.server, '/api/state', cookie).then(response => response.json())).server.frps.state).toBe('stopped')
  })

  test('accepts the external HTTPS origin from a same-host TLS proxy', async () => {
    const value = await fixture()
    const cookie = await login(value.server)
    const externalOrigin = new URL(value.server.url)
    externalOrigin.protocol = 'https:'
    const accepted = await fetch(new URL('/api/clients', value.server.url), {
      method: 'POST',
      headers: { 'Cookie': cookie, 'Origin': externalOrigin.origin, 'Content-Type': 'application/json' },
      body: JSON.stringify({ remark: 'Behind NPM' }),
    })
    expect(accepted.status).toBe(201)

    const rejected = await fetch(new URL('/api/clients', value.server.url), {
      method: 'POST',
      headers: { 'Cookie': cookie, 'Origin': 'https://attacker.example', 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    })
    expect(rejected.status).toBe(403)
  })

  test('scopes HTTP resources by account and reserves administration for admins', async () => {
    const value = await fixture()
    const adminCookie = await login(value.server)
    for (const username of ['alice', 'bob']) {
      const response = await request(value.server, '/api/accounts', adminCookie, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password: `${username}-secret`, role: 'user' }),
      })
      expect(response.status).toBe(201)
    }
    const aliceCookie = await login(value.server, 'alice', 'alice-secret')
    const bobCookie = await login(value.server, 'bob', 'bob-secret')
    const created = await request(value.server, '/api/clients', aliceCookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ remark: 'Alice client' }),
    })
    const client = (await created.json()).client
    const tunnel = await request(value.server, `/api/clients/${client.id}/tunnels`, aliceCookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ protocol: 'http', hostname: 'alice.example.com', localPort: 3000 }),
    }).then(response => response.json()).then(body => body.tunnel)

    expect((await request(value.server, '/api/clients', bobCookie).then(response => response.json())).clients).toEqual([])
    expect((await request(value.server, `/api/clients/${client.id}`, bobCookie)).status).toBe(404)
    expect((await request(value.server, `/api/clients/${client.id}/tunnels/import/preview`, bobCookie, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source: 'proxies = []' }),
    })).status).toBe(404)
    expect((await request(value.server, `/api/tunnels/${tunnel.id}`, bobCookie, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: false }),
    })).status).toBe(404)
    expect((await request(value.server, '/api/accounts', bobCookie)).status).toBe(403)
    expect((await request(value.server, '/api/server/frp/stop', bobCookie, { method: 'POST' })).status).toBe(403)
    expect((await request(value.server, '/api/server/frps/config/custom-404-page', bobCookie)).status).toBe(403)
    expect((await request(value.server, '/api/state', bobCookie).then(response => response.json())).server).toBeUndefined()

    const adminState = await request(value.server, '/api/state', adminCookie).then(response => response.json())
    expect(adminState.server.settings.adminUser).toBe('admin')
    const adminClient = await request(value.server, `/api/clients/${client.id}`, adminCookie).then(response => response.json())
    expect(adminClient.client.owner.username).toBe('alice')
  })
})
