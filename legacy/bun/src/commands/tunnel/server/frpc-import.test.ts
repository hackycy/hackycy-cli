import { describe, expect, test } from 'bun:test'
import { parseFrpcTunnelImport, selectedImportedTunnels, tunnelImportPreview } from './frpc-import'

const SOURCE = `
serverAddr = "tunnel.example.com"
serverPort = 7000

[[proxies]]
name = "web"
type = "http"
localIP = "app"
localPort = 3000
customDomains = ["app.example.com", "alias.example.com"]
locations = ["/api", "/admin"]
httpUser = "operator"
httpPassword = "secret-value"
hostHeaderRewrite = "backend.example.com"
requestHeaders.set = { X-Forwarded-By = "ycy" }
responseHeaders.set = { X-Tunnel = "web" }

[proxies.transport]
useEncryption = true
useCompression = true
bandwidthLimit = "2MB"
bandwidthLimitMode = "server"
proxyProtocolVersion = "v2"

[proxies.healthCheck]
type = "http"
path = "/health"
intervalSeconds = 10
timeoutSeconds = 3
maxFailed = 2
httpHeaders = [{ name = "X-Probe", value = "ycy" }]

[[proxies]]
name = "database"
type = "tcp"
localPort = 5432
remotePort = 20001

[[proxies]]
name = "dns"
type = "udp"
localPort = 53
remotePort = 20002
`

describe('frpc tunnel import', () => {
  test('maps supported proxy fields, expands locations, and redacts preview credentials', () => {
    const imported = parseFrpcTunnelImport(SOURCE)

    expect(imported.candidates).toHaveLength(4)
    expect(imported.candidates[0]!.input).toMatchObject({
      protocol: 'http',
      customDomains: ['app.example.com', 'alias.example.com'],
      location: '/api',
      localHost: 'app',
      localPort: 3000,
      enabled: false,
      options: {
        transport: { useEncryption: true, useCompression: true, bandwidthLimit: { value: 2, unit: 'MB', mode: 'server' }, proxyProtocolVersion: 'v2' },
        healthCheck: { type: 'http', path: '/health', intervalSeconds: 10, timeoutSeconds: 3, maxFailed: 2, headers: [{ name: 'X-Probe', value: 'ycy' }] },
        http: {
          basicAuth: { username: 'operator', password: 'secret-value' },
          hostHeaderRewrite: 'backend.example.com',
          requestHeaders: [{ name: 'X-Forwarded-By', value: 'ycy' }],
          responseHeaders: [{ name: 'X-Tunnel', value: 'web' }],
        },
      },
    })
    expect(imported.candidates[1]!.input).toMatchObject({ protocol: 'http', location: '/admin', enabled: false })
    expect(imported.candidates[2]!.input).toMatchObject({ protocol: 'tcp', serverPort: 20001, localHost: '127.0.0.1', enabled: false })
    expect(imported.candidates[3]!.input).toMatchObject({ protocol: 'udp', serverPort: 20002, enabled: false })

    const preview = tunnelImportPreview(imported)
    expect(preview.candidates[0]!.basicAuth).toEqual({ username: 'operator', passwordConfigured: true })
    expect(JSON.stringify(preview)).not.toContain('secret-value')
    expect(preview.ignored).toContainEqual({ reason: 'Client connection settings are not imported' })
  })

  test('reports unsupported proxies and rejects invalid selections without importing them', () => {
    const imported = parseFrpcTunnelImport(`
[[proxies]]
name = "private"
type = "stcp"
localPort = 7000
secretKey = "not-imported"

[[proxies]]
name = "catch-all"
type = "http"
localPort = 3000
customDomains = ["catch.example.com"]
plugin = { type = "static_file" }
`)

    expect(imported.candidates).toHaveLength(1)
    expect(imported.candidates[0]!.input.location).toBeNull()
    expect(imported.ignored).toEqual(expect.arrayContaining([
      expect.objectContaining({ proxy: 'private', reason: expect.stringContaining('unsupported proxy type') }),
      expect.objectContaining({ proxy: 'catch-all', reason: expect.stringContaining('plugin') }),
    ]))
    expect(() => selectedImportedTunnels(imported, [])).toThrow('Select at least one')
    expect(() => selectedImportedTunnels(imported, ['missing'])).toThrow('no longer valid')
  })

  test('rejects TOML without FRP v1 proxy definitions', () => {
    expect(() => parseFrpcTunnelImport('[common]\nserver_addr = "example.com"')).toThrow('proxies array')
    expect(() => parseFrpcTunnelImport('proxies = [')).toThrow('valid TOML')
  })
})
