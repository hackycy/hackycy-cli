import type { RememberedTunnelConnection } from '../../config/tunnel'
import type { ClientTunnelConfig, ServerTunnelConfig } from './types'
import { readFile } from 'node:fs/promises'
import { isIP } from 'node:net'
import path from 'node:path'
import process from 'node:process'
import { readTunnelConnectionCatalog } from '../../config/tunnel'
import { clientStateDirectory, defaultServerDataDirectory } from './paths'
import { TunnelError } from './types'

export const DEFAULT_TUNNEL_SERVER = ''

export interface ServerOptionInput {
  address?: string
  controlPort?: string | number
  frpPort?: string | number
  httpPort?: string | number
  portRange?: string
  advertiseFrpAddr?: string
  dataDir?: string
  sessionIdleDays?: string | number
}

export interface ClientOptionInput {
  server?: string
  token?: string
}

export interface ResolvedClientConfig {
  config: ClientTunnelConfig
  rememberOnAuthentication: boolean
}

export interface ResolveClientConfigOptions {
  selectConnection?: (connections: readonly RememberedTunnelConnection[]) => Promise<string | undefined>
}

function option(input: string | number | undefined, env: NodeJS.ProcessEnv, name: string, fallback: string | number): string | number {
  if (input !== undefined)
    return input
  if (env[name] !== undefined)
    return env[name]!
  return fallback
}

function port(value: string | number, label: string): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  if (!Number.isSafeInteger(parsed) || parsed < 1 || parsed > 65535)
    throw new TunnelError('INVALID_CONFIG', `${label} must be an integer from 1 through 65535`)
  return parsed
}

function sessionIdleLifetime(value: string | number): number {
  const days = typeof value === 'number' ? value : Number(value)
  if (!Number.isSafeInteger(days) || days < 1)
    throw new TunnelError('INVALID_CONFIG', 'Session idle lifetime must be a positive integer number of days')
  return days * 24 * 60 * 60 * 1000
}

export function parsePortRange(value: string): { start: number, end: number } {
  const match = /^(\d+)-(\d+)$/.exec(value.trim())
  if (!match)
    throw new TunnelError('INVALID_CONFIG', 'Server Port Pool must use start-end syntax')
  const start = port(match[1]!, 'Server Port Pool start')
  const end = port(match[2]!, 'Server Port Pool end')
  if (start > end)
    throw new TunnelError('INVALID_CONFIG', 'Server Port Pool start must not exceed its end')
  return { start, end }
}

export function parseHostPort(value: string): { host: string, port: number } {
  let host: string
  let rawPort: string
  if (value.startsWith('[')) {
    const separator = value.indexOf(']:')
    host = separator > 1 ? value.slice(1, separator) : ''
    rawPort = separator > 1 ? value.slice(separator + 2) : ''
    if (isIP(host) !== 6)
      throw new TunnelError('INVALID_CONFIG', 'Advertised FRP address must be host:port or [IPv6]:port')
  }
  else {
    const separator = value.lastIndexOf(':')
    if (separator <= 0 || value.indexOf(':') !== separator) {
      throw new TunnelError('INVALID_CONFIG', 'Advertised FRP address must be host:port or [IPv6]:port')
    }
    host = value.slice(0, separator).trim()
    rawPort = value.slice(separator + 1)
  }
  if (!host || /[/?#@]/.test(host))
    throw new TunnelError('INVALID_CONFIG', 'Advertised FRP host is invalid')
  return { host, port: port(rawPort, 'Advertised FRP port') }
}

export function resolveServerConfig(input: ServerOptionInput, env: NodeJS.ProcessEnv = process.env): ServerTunnelConfig {
  const address = String(option(input.address, env, 'YCY_TUNNEL_ADDRESS', '0.0.0.0')).trim()
  if (!address)
    throw new TunnelError('INVALID_CONFIG', 'Tunnel server address is required')
  const dataDir = path.resolve(String(option(input.dataDir, env, 'YCY_TUNNEL_DATA_DIR', defaultServerDataDirectory(env))))
  const advertised = option(input.advertiseFrpAddr, env, 'YCY_TUNNEL_ADVERTISE_FRP_ADDR', '')
  const frpToken = env.YCY_TUNNEL_FRP_TOKEN?.trim()
  if (env.YCY_TUNNEL_FRP_TOKEN !== undefined && !frpToken)
    throw new TunnelError('INVALID_CONFIG', 'FRP Token must not be empty')
  const adminUser = env.YCY_TUNNEL_ADMIN_USER ?? 'admin'
  if (!/^[\w.-]{1,64}$/.test(adminUser))
    throw new TunnelError('INVALID_CONFIG', 'Environment administrator username must contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens')
  const adminPassword = env.YCY_TUNNEL_ADMIN_PASSWORD
  if (!adminPassword || adminPassword.length < 5 || adminPassword.length > 256)
    throw new TunnelError('INVALID_CONFIG', 'YCY_TUNNEL_ADMIN_PASSWORD must contain 5-256 characters')
  const config: ServerTunnelConfig = {
    address,
    controlPort: port(option(input.controlPort, env, 'YCY_TUNNEL_CONTROL_PORT', 7500), 'Control port'),
    frpPort: port(option(input.frpPort, env, 'YCY_TUNNEL_FRP_PORT', 7000), 'FRP bind port'),
    httpPort: port(option(input.httpPort, env, 'YCY_TUNNEL_HTTP_PORT', 8080), 'FRP HTTP port'),
    portRange: parsePortRange(String(option(input.portRange, env, 'YCY_TUNNEL_PORT_RANGE', '20000-20100'))),
    ...(String(advertised).trim() ? { advertiseFrpAddress: parseHostPort(String(advertised).trim()) } : {}),
    ...(frpToken ? { frpToken } : {}),
    dataDir,
    adminUser,
    adminPassword,
    sessionIdleLifetimeMs: sessionIdleLifetime(option(input.sessionIdleDays, env, 'YCY_TUNNEL_SESSION_IDLE_DAYS', 7)),
  }
  const listenerPorts = [config.controlPort, config.frpPort, config.httpPort]
  if (new Set(listenerPorts).size !== listenerPorts.length)
    throw new TunnelError('INVALID_CONFIG', 'Control, FRP bind, and FRP HTTP listener ports must be distinct')
  const overlapping = listenerPorts.find(listenerPort => listenerPort >= config.portRange.start && listenerPort <= config.portRange.end)
  if (overlapping !== undefined)
    throw new TunnelError('INVALID_CONFIG', `Server Port Pool must not include listener port ${overlapping}`)
  return config
}

export function normalizeControlPlaneUrl(value: string): URL {
  const explicit = /^[a-z][a-z\d+.-]*:\/\//i.test(value)
  let url: URL
  try {
    url = new URL(explicit ? value : `https://${value}`)
  }
  catch {
    throw new TunnelError('INVALID_CONFIG', 'Control plane must be a valid HTTP or HTTPS address')
  }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || !url.hostname)
    throw new TunnelError('INVALID_CONFIG', 'Control plane must be an HTTP or HTTPS origin without credentials, query, or fragment')
  if (url.pathname !== '/' && url.pathname !== '')
    throw new TunnelError('INVALID_CONFIG', 'Control plane must not include a path')
  url.pathname = '/'
  return url
}

export function maskTunnelToken(token: string): string {
  if (token.length <= 12)
    return '*'.repeat(token.length)
  return `${token.slice(0, 8)}********${token.slice(-4)}`
}

async function selectRememberedConnection(
  connections: readonly RememberedTunnelConnection[],
  selectConnection: ResolveClientConfigOptions['selectConnection'],
): Promise<RememberedTunnelConnection | null | undefined> {
  if (connections.length === 0)
    return undefined
  if (connections.length === 1)
    return connections[0]!
  if (!selectConnection) {
    throw new TunnelError(
      'INVALID_CONFIG',
      'Multiple remembered tunnel connections match; provide both --server and --token in a non-interactive environment',
    )
  }
  const selectedId = await selectConnection(connections)
  if (selectedId === undefined)
    return null
  const selected = connections.find(connection => connection.id === selectedId)
  if (!selected)
    throw new TunnelError('INVALID_CONFIG', 'The selected remembered tunnel connection is unavailable')
  return selected
}

export async function resolveClientConfig(
  input: ClientOptionInput,
  env: NodeJS.ProcessEnv = process.env,
  defaultServer: string = DEFAULT_TUNNEL_SERVER,
  options: ResolveClientConfigOptions = {},
): Promise<ResolvedClientConfig | undefined> {
  const catalog = await readTunnelConnectionCatalog(env)
  const rawServer = input.server ?? env.YCY_TUNNEL_SERVER
  if (rawServer !== undefined && !rawServer.trim())
    throw new TunnelError('INVALID_CONFIG', 'Control plane must not be empty')
  let server = rawServer?.trim() ? normalizeControlPlaneUrl(rawServer.trim()) : undefined
  let token = input.token ?? env.YCY_TUNNEL_TOKEN
  if (token === undefined && env.YCY_TUNNEL_TOKEN_FILE) {
    try {
      token = await readFile(path.resolve(env.YCY_TUNNEL_TOKEN_FILE), 'utf8')
    }
    catch (cause) {
      throw new TunnelError('INVALID_CONFIG', `Could not read Client Token file: ${cause instanceof Error ? cause.message : String(cause)}`)
    }
  }
  const tokenWasProvided = token !== undefined
  token = token?.trim()
  if (tokenWasProvided && !token)
    throw new TunnelError('INVALID_CONFIG', 'Client Token must not be empty')

  if (server && !token) {
    const selected = await selectRememberedConnection(
      catalog.connections.filter(connection => connection.server === server!.origin),
      options.selectConnection,
    )
    if (selected === null)
      return undefined
    token = selected?.token
  }
  else if (!server && token) {
    let candidates = catalog.connections.filter(connection => connection.token === token)
    if (candidates.length === 0) {
      const seenServers = new Set<string>()
      candidates = catalog.connections.flatMap((connection) => {
        if (seenServers.has(connection.server))
          return []
        seenServers.add(connection.server)
        const candidateServer = new URL(connection.server)
        return [{
          ...connection,
          id: catalog.instanceId(candidateServer, token!),
          token: token!,
        }]
      })
    }
    if (candidates.length > 0) {
      const selected = await selectRememberedConnection(candidates, options.selectConnection)
      if (selected === null)
        return undefined
      server = selected ? new URL(selected.server) : undefined
    }
    else if (defaultServer.trim()) {
      server = normalizeControlPlaneUrl(defaultServer.trim())
    }
  }
  else if (!server && !token && catalog.connections.length > 0) {
    const selected = await selectRememberedConnection(catalog.connections, options.selectConnection)
    if (selected === null)
      return undefined
    if (selected) {
      server = new URL(selected.server)
      token = selected.token
    }
  }

  if (!server && defaultServer.trim())
    server = normalizeControlPlaneUrl(defaultServer.trim())
  if (!server)
    throw new TunnelError('INVALID_CONFIG', 'Control plane is required through --server, YCY_TUNNEL_SERVER, a remembered connection, or DEFAULT_TUNNEL_SERVER')
  if (!token)
    throw new TunnelError('INVALID_CONFIG', 'Client Token is required through --token, YCY_TUNNEL_TOKEN, YCY_TUNNEL_TOKEN_FILE, or a matching remembered connection')

  const id = catalog.instanceId(server, token)
  const remembered = catalog.connections.some(connection => connection.id === id)

  return {
    config: { server, token, stateDir: path.join(clientStateDirectory(env), id) },
    rememberOnAuthentication: input.token !== undefined || remembered,
  }
}
