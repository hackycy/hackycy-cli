import type { TunnelView } from './api'
import { describe, expect, test } from 'bun:test'
import { buildTunnelPayload, createTunnelSchema, draftToTunnelForm } from './tunnel-form'

function validValues(): ReturnType<typeof draftToTunnelForm> {
  return {
    ...draftToTunnelForm(),
    label: 'Public app',
    customDomains: [{ value: 'routes.example.com' }],
    localPort: '3000',
  }
}

describe('Tunnel form schema', () => {
  test('maps an existing HTTP tunnel into editable defaults without exposing its password', () => {
    const initial: TunnelView = {
      id: 'tunnel-1',
      label: 'Existing route',
      protocol: 'http',
      customDomains: ['routes.example.com'],
      location: '/api',
      serverPort: null,
      localHost: '127.0.0.1',
      localPort: 8080,
      enabled: true,
      createdAt: '2026-01-01T00:00:00.000Z',
      updatedAt: '2026-01-01T00:00:00.000Z',
      state: 'Applied',
      options: {
        transport: { useEncryption: true, useCompression: false, bandwidthLimit: { value: 3, unit: 'MB', mode: 'server' }, proxyProtocolVersion: 'v2' },
        healthCheck: { type: 'http', path: '/ready', intervalSeconds: 20, timeoutSeconds: 5, maxFailed: 2, headers: [{ name: 'X-Health', value: 'yes' }] },
        http: { basicAuth: { username: 'proxy', passwordConfigured: true }, hostHeaderRewrite: 'origin.internal', requestHeaders: [{ name: 'X-Request', value: '1' }], responseHeaders: [{ name: 'X-Response', value: '1' }] },
      },
    }
    expect(draftToTunnelForm(initial)).toMatchObject({
      customDomains: [{ value: 'routes.example.com' }],
      location: '/api',
      localPort: '8080',
      bandwidthValue: '3',
      bandwidthMode: 'server',
      healthPath: '/ready',
      authEnabled: true,
      authUsername: 'proxy',
      authPassword: '',
    })
  })

  test('accepts the default HTTP form after a local port and domain are supplied', () => {
    expect(createTunnelSchema().safeParse(validValues()).success).toBe(true)
  })

  test('requires an HTTP domain and validates route locations', () => {
    const values = validValues()
    values.customDomains = [{ value: ' ' }]
    values.location = 'missing-leading-slash'
    const result = createTunnelSchema().safeParse(values)
    expect(result.success).toBe(false)
    if (!result.success)
      expect(result.error.issues.map(issue => issue.path[0])).toEqual(expect.arrayContaining(['customDomains', 'location']))
  })

  test('keeps an existing Basic Auth password optional while requiring a new one', () => {
    const values = validValues()
    values.authEnabled = true
    values.authUsername = 'proxy'
    expect(createTunnelSchema().safeParse(values).success).toBe(false)
    expect(createTunnelSchema({ hasExistingBasicAuth: true }).safeParse(values).success).toBe(true)
  })

  test('validates health timing and HTTP path only when health checks are enabled', () => {
    const values = validValues()
    values.healthEnabled = true
    values.healthType = 'http'
    values.healthInterval = '0'
    values.healthPath = 'health'
    const result = createTunnelSchema().safeParse(values)
    expect(result.success).toBe(false)
    if (!result.success)
      expect(result.error.issues.map(issue => issue.path[0])).toEqual(expect.arrayContaining(['healthInterval', 'healthPath']))
  })
})

describe('Tunnel form payload', () => {
  test('trims and filters dynamic headers and domains without changing the API shape', () => {
    const values = validValues()
    values.customDomains = [{ value: ' routes.example.com ' }, { value: ' ' }]
    values.requestHeaders = [{ name: ' X-Test ', value: ' value ' }, { name: ' ', value: 'ignored' }]
    values.location = ' /api '
    values.bandwidthEnabled = true
    values.bandwidthValue = '2.5'
    values.healthEnabled = true
    values.healthType = 'http'
    values.healthHeaders = [{ name: ' X-Health ', value: ' ok ' }]
    const payload = buildTunnelPayload(values)
    expect(payload.customDomains).toEqual(['routes.example.com'])
    expect(payload.location).toBe('/api')
    expect(payload.options.transport.bandwidthLimit).toEqual({ value: 2.5, unit: 'MB', mode: 'client' })
    expect(payload.options.healthCheck).toMatchObject({ type: 'http', headers: [{ name: 'X-Health', value: 'ok' }] })
    expect(payload.options.http?.requestHeaders).toEqual([{ name: 'X-Test', value: 'value' }])
  })

  test('omits public HTTP fields and server ports for TCP/UDP payloads', () => {
    const values = validValues()
    values.protocol = 'tcp'
    values.serverPort = '4200'
    const payload = buildTunnelPayload(values)
    expect(payload.customDomains).toBeUndefined()
    expect(payload.location).toBeNull()
    expect(payload.serverPort).toBe(4200)
    expect(payload.options.http).toBeNull()
  })
})
