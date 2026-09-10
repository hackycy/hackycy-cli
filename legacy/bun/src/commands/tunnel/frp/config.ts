import type { LogLevel } from '../../../shared/log'
import type { FrpcDesiredConfiguration, ServerTunnelConfig, TunnelHeader, TunnelProtocol } from '../types'
import type { TomlDocument } from './toml'
import { tomlCodec } from './toml'

export interface FrpConfigurationVerificationOptions {
  signal?: AbortSignal
  timeoutMs?: number
}

interface FrpAuthenticationToml extends TomlDocument {
  method: 'token'
  token: string
}

interface FrpLogToml extends TomlDocument {
  to: 'console'
  level: LogLevel
}

interface FrpPortRangeToml extends TomlDocument {
  start: number
  end: number
}

interface FrpTransportToml extends TomlDocument {
  bandwidthLimit?: string
  bandwidthLimitMode?: 'client' | 'server'
  useEncryption?: true
  useCompression?: true
  proxyProtocolVersion?: 'v1' | 'v2'
}

interface FrpHealthCheckToml extends TomlDocument {
  type: 'http' | 'tcp'
  timeoutSeconds: number
  maxFailed: number
  intervalSeconds: number
  path?: string
  httpHeaders?: FrpHeaderToml[]
}

interface FrpHeaderToml extends TomlDocument {
  name: string
  value: string
}

interface FrpHeaderSetToml extends TomlDocument {
  set: TomlDocument
}

interface FrpProxyToml extends TomlDocument {
  name: string
  type: TunnelProtocol
  localIP: string
  localPort: number
  remotePort?: number
  customDomains?: string[]
  locations?: string[]
  httpUser?: string
  httpPassword?: string
  hostHeaderRewrite?: string
  transport?: FrpTransportToml
  healthCheck?: FrpHealthCheckToml
  requestHeaders?: FrpHeaderSetToml
  responseHeaders?: FrpHeaderSetToml
}

interface FrpsTomlDocument extends TomlDocument {
  bindAddr: string
  bindPort: number
  vhostHTTPPort: number
  custom404Page: string
  auth: FrpAuthenticationToml
  allowPorts: FrpPortRangeToml[]
  log: FrpLogToml
}

interface FrpcTomlDocument extends TomlDocument {
  serverAddr: string
  serverPort: number
  user: string
  loginFailExit: false
  auth: FrpAuthenticationToml
  log: FrpLogToml
  proxies?: FrpProxyToml[]
}

type FrpcTunnel = FrpcDesiredConfiguration['snapshot']['tunnels'][number]

export async function verifyFrpConfiguration(binaryPath: string, configurationPath: string, options: FrpConfigurationVerificationOptions = {}): Promise<void> {
  options.signal?.throwIfAborted()
  const child = Bun.spawn([binaryPath, 'verify', '-c', configurationPath], { stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  let timedOut = false
  const timeout = setTimeout(() => {
    timedOut = true
    child.kill('SIGKILL')
  }, options.timeoutMs ?? 10_000)
  const abort = (): void => child.kill('SIGTERM')
  options.signal?.addEventListener('abort', abort, { once: true })
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    options.signal?.throwIfAborted()
    if (timedOut)
      throw new Error(`FRP configuration verification timed out after ${options.timeoutMs ?? 10_000}ms`)
    if (code !== 0)
      throw new Error((stderr || stdout || `FRP configuration verification exited with code ${code}`).trim())
  }
  finally {
    clearTimeout(timeout)
    options.signal?.removeEventListener('abort', abort)
  }
}

function headerSet(headers: TunnelHeader[]): FrpHeaderSetToml | undefined {
  if (!headers.length)
    return undefined
  return { set: Object.fromEntries(headers.map(header => [header.name, header.value])) }
}

function applyProxyOptions(proxy: FrpProxyToml, tunnel: FrpcTunnel): void {
  const { transport, healthCheck } = tunnel.options
  const transportToml: FrpTransportToml = {}
  if (transport.bandwidthLimit) {
    transportToml.bandwidthLimit = `${transport.bandwidthLimit.value}${transport.bandwidthLimit.unit}`
    transportToml.bandwidthLimitMode = transport.bandwidthLimit.mode
  }
  if (transport.useEncryption)
    transportToml.useEncryption = true
  if (transport.useCompression)
    transportToml.useCompression = true
  if (transport.proxyProtocolVersion)
    transportToml.proxyProtocolVersion = transport.proxyProtocolVersion
  if (Object.keys(transportToml).length)
    proxy.transport = transportToml

  if (!healthCheck)
    return
  const healthCheckToml: FrpHealthCheckToml = {
    type: healthCheck.type,
    timeoutSeconds: healthCheck.timeoutSeconds,
    maxFailed: healthCheck.maxFailed,
    intervalSeconds: healthCheck.intervalSeconds,
  }
  if (healthCheck.type === 'http') {
    healthCheckToml.path = healthCheck.path
    if (healthCheck.headers.length)
      healthCheckToml.httpHeaders = healthCheck.headers.map(header => ({ name: header.name, value: header.value }))
  }
  proxy.healthCheck = healthCheckToml
}

function buildProxy(tunnel: FrpcTunnel): FrpProxyToml {
  const proxy: FrpProxyToml = {
    name: `t_${tunnel.id.replace(/[^\w-]/g, '_')}`,
    type: tunnel.protocol,
    localIP: tunnel.localHost,
    localPort: tunnel.localPort,
  }
  applyProxyOptions(proxy, tunnel)
  if (tunnel.protocol !== 'http') {
    proxy.remotePort = tunnel.serverPort
    return proxy
  }

  proxy.customDomains = tunnel.customDomains
  if (tunnel.location !== null)
    proxy.locations = [tunnel.location]
  const http = tunnel.options.http!
  if (http.basicAuth) {
    proxy.httpUser = http.basicAuth.username
    proxy.httpPassword = http.basicAuth.password
  }
  if (http.hostHeaderRewrite)
    proxy.hostHeaderRewrite = http.hostHeaderRewrite
  const requestHeaders = headerSet(http.requestHeaders)
  if (requestHeaders)
    proxy.requestHeaders = requestHeaders
  const responseHeaders = headerSet(http.responseHeaders)
  if (responseHeaders)
    proxy.responseHeaders = responseHeaders
  return proxy
}

function buildFrpsDocument(config: ServerTunnelConfig, internalFrpToken: string, custom404PagePath: string, logLevel: LogLevel): FrpsTomlDocument {
  return {
    bindAddr: config.address,
    bindPort: config.frpPort,
    vhostHTTPPort: config.httpPort,
    custom404Page: custom404PagePath,
    auth: { method: 'token', token: internalFrpToken },
    allowPorts: [{ start: config.portRange.start, end: config.portRange.end }],
    log: { to: 'console', level: logLevel },
  }
}

function buildFrpcDocument(input: FrpcDesiredConfiguration, logLevel: LogLevel): FrpcTomlDocument {
  const proxies = input.snapshot.tunnels.filter(tunnel => tunnel.enabled).map(buildProxy)
  return {
    serverAddr: input.advertisedFrpHost,
    serverPort: input.advertisedFrpPort,
    user: `ycy_${input.snapshot.clientKey.replace(/[^\w-]/g, '_')}`,
    loginFailExit: false,
    auth: { method: 'token', token: input.internalFrpToken },
    log: { to: 'console', level: logLevel },
    ...(proxies.length ? { proxies } : {}),
  }
}

export function renderFrpsConfig(config: ServerTunnelConfig, internalFrpToken: string, custom404PagePath: string, logLevel: LogLevel = 'warn'): string {
  return tomlCodec.stringify(buildFrpsDocument(config, internalFrpToken, custom404PagePath, logLevel))
}

export function renderFrpcConfig(input: FrpcDesiredConfiguration, logLevel: LogLevel = 'warn'): string {
  return tomlCodec.stringify(buildFrpcDocument(input, logLevel))
}
