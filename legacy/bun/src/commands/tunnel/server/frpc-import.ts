import type { TunnelHttpOptionsInput, TunnelOptionsInput, TunnelTransportOptionsInput } from '../definition'
import type { TunnelHeader, TunnelProtocol } from '../types'
import type { TunnelMutationInput } from './control-plane'
import { tomlCodec } from '../frp/toml'
import { TunnelError } from '../types'

interface ImportedTunnelCandidate {
  id: string
  input: TunnelMutationInput
}

export interface TunnelImportNotice {
  proxy?: string
  reason: string
}

export interface FrpcTunnelImport {
  candidates: ImportedTunnelCandidate[]
  ignored: TunnelImportNotice[]
}

export interface TunnelImportCandidateView {
  id: string
  label: string
  protocol: TunnelProtocol
  customDomains?: string[]
  location?: string | null
  serverPort?: number
  localHost: string
  localPort: number
  basicAuth: { username: string, passwordConfigured: true } | null
}

export interface TunnelImportPreview {
  candidates: TunnelImportCandidateView[]
  ignored: TunnelImportNotice[]
}

function table(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    return undefined
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null ? value as Record<string, unknown> : undefined
}

function string(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function integer(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : undefined
}

function proxyLabel(proxy: Record<string, unknown>, index: number): string {
  const value = string(proxy.name)?.trim()
  return value || `Proxy ${index + 1}`
}

function notice(notices: TunnelImportNotice[], proxy: string, reason: string): void {
  notices.push({ proxy, reason })
}

function headers(value: unknown, proxy: string, label: string, notices: TunnelImportNotice[]): TunnelHeader[] | undefined {
  const set = table(table(value)?.set)
  if (!set) {
    if (value !== undefined)
      notice(notices, proxy, `${label} were ignored because they are not a string set`)
    return undefined
  }
  const result: TunnelHeader[] = []
  let ignored = false
  for (const [name, headerValue] of Object.entries(set)) {
    if (typeof headerValue === 'string')
      result.push({ name, value: headerValue })
    else
      ignored = true
  }
  if (ignored)
    notice(notices, proxy, `Some ${label.toLowerCase()} were ignored because their values are not strings`)
  return result
}

function healthHeaders(value: unknown, proxy: string, notices: TunnelImportNotice[]): TunnelHeader[] | undefined {
  if (!Array.isArray(value)) {
    if (value !== undefined)
      notice(notices, proxy, 'Health check headers were ignored because they are not an array')
    return undefined
  }
  const result: TunnelHeader[] = []
  let ignored = false
  for (const entry of value) {
    const header = table(entry)
    const name = string(header?.name)
    const headerValue = string(header?.value)
    if (name && headerValue !== undefined)
      result.push({ name, value: headerValue })
    else
      ignored = true
  }
  if (ignored)
    notice(notices, proxy, 'Some health check headers were ignored because they are invalid')
  return result
}

function transport(value: unknown, proxy: string, notices: TunnelImportNotice[]): TunnelTransportOptionsInput | undefined {
  const source = table(value)
  if (!source) {
    if (value !== undefined)
      notice(notices, proxy, 'Transport settings were ignored because they are not a TOML table')
    return undefined
  }
  const result: TunnelTransportOptionsInput = {}
  for (const field of ['useEncryption', 'useCompression'] as const) {
    if (typeof source[field] === 'boolean')
      result[field] = source[field]
    else if (source[field] !== undefined)
      notice(notices, proxy, `${field} was ignored because it is not a boolean`)
  }

  if (source.bandwidthLimit !== undefined) {
    const match = /^\s*(\d+(?:\.\d+)?)\s*(KB|MB)\s*$/.exec(string(source.bandwidthLimit) ?? '')
    if (!match || Number(match[1]) <= 0) {
      notice(notices, proxy, 'Bandwidth limit was ignored because it is not a positive KB or MB value')
    }
    else {
      const mode = source.bandwidthLimitMode === undefined || source.bandwidthLimitMode === 'client'
        ? 'client'
        : source.bandwidthLimitMode === 'server'
          ? 'server'
          : undefined
      if (!mode)
        notice(notices, proxy, 'Bandwidth limit was ignored because its mode is not client or server')
      else
        result.bandwidthLimit = { value: Number(match[1]), unit: match[2] as 'KB' | 'MB', mode }
    }
  }
  else if (source.bandwidthLimitMode !== undefined) {
    notice(notices, proxy, 'Bandwidth limit mode was ignored because no bandwidth limit is configured')
  }

  if (source.proxyProtocolVersion === 'v1' || source.proxyProtocolVersion === 'v2')
    result.proxyProtocolVersion = source.proxyProtocolVersion
  else if (source.proxyProtocolVersion !== undefined)
    notice(notices, proxy, 'Proxy Protocol version was ignored because it is not v1 or v2')
  return Object.keys(result).length ? result : undefined
}

function healthCheck(value: unknown, proxy: string, notices: TunnelImportNotice[]): TunnelOptionsInput['healthCheck'] {
  const source = table(value)
  if (!source) {
    if (value !== undefined)
      notice(notices, proxy, 'Health check was ignored because it is not a TOML table')
    return undefined
  }
  const intervalSeconds = integer(source.intervalSeconds)
  const timeoutSeconds = integer(source.timeoutSeconds)
  const maxFailed = integer(source.maxFailed)
  if (!intervalSeconds || !timeoutSeconds || !maxFailed || !['tcp', 'http'].includes(string(source.type) ?? '')) {
    notice(notices, proxy, 'Health check was ignored because it is incomplete or unsupported')
    return undefined
  }
  if (source.type === 'tcp')
    return { type: 'tcp', intervalSeconds, timeoutSeconds, maxFailed }

  const path = string(source.path)
  if (!path?.startsWith('/')) {
    notice(notices, proxy, 'HTTP health check was ignored because it has no valid path')
    return undefined
  }
  const httpHeaders = healthHeaders(source.httpHeaders, proxy, notices)
  return {
    type: 'http',
    path,
    intervalSeconds,
    timeoutSeconds,
    maxFailed,
    ...(httpHeaders ? { headers: httpHeaders } : {}),
  }
}

function httpOptions(source: Record<string, unknown>, proxy: string, notices: TunnelImportNotice[]): TunnelHttpOptionsInput | undefined {
  const result: TunnelHttpOptionsInput = {}
  const username = string(source.httpUser)
  const password = string(source.httpPassword)
  if (username !== undefined || password !== undefined) {
    if (username?.trim() && password)
      result.basicAuth = { username, password }
    else
      notice(notices, proxy, 'HTTP Basic Auth was ignored because both username and password are required')
  }
  if (source.hostHeaderRewrite !== undefined) {
    const value = string(source.hostHeaderRewrite)?.trim()
    if (value)
      result.hostHeaderRewrite = value
    else
      notice(notices, proxy, 'Host Header Rewrite was ignored because it is not a non-empty string')
  }
  const requestHeaders = headers(source.requestHeaders, proxy, 'Request headers', notices)
  if (requestHeaders)
    result.requestHeaders = requestHeaders
  const responseHeaders = headers(source.responseHeaders, proxy, 'Response headers', notices)
  if (responseHeaders)
    result.responseHeaders = responseHeaders
  return Object.keys(result).length ? result : undefined
}

function unsupportedFields(proxy: Record<string, unknown>, label: string, notices: TunnelImportNotice[]): void {
  const supported = new Set([
    'name',
    'type',
    'localIP',
    'localPort',
    'remotePort',
    'customDomains',
    'locations',
    'transport',
    'healthCheck',
    'httpUser',
    'httpPassword',
    'hostHeaderRewrite',
    'requestHeaders',
    'responseHeaders',
  ])
  const fields = Object.keys(proxy).filter(field => !supported.has(field))
  if (fields.length)
    notice(notices, label, `Ignored unsupported fields: ${fields.join(', ')}`)
}

function proxyCandidates(proxy: Record<string, unknown>, index: number, ignored: TunnelImportNotice[]): ImportedTunnelCandidate[] {
  const label = proxyLabel(proxy, index)
  const tunnelProtocol = string(proxy.type)?.toLowerCase()
  if (tunnelProtocol !== 'http' && tunnelProtocol !== 'tcp' && tunnelProtocol !== 'udp') {
    notice(ignored, label, `Ignored unsupported proxy type${tunnelProtocol ? `: ${tunnelProtocol}` : ''}`)
    return []
  }
  if (label.length > 100) {
    notice(ignored, label, 'Ignored proxy because its name is longer than 100 characters')
    return []
  }
  const localHost = proxy.localIP === undefined ? '127.0.0.1' : string(proxy.localIP)?.trim()
  const localPort = integer(proxy.localPort)
  if (!localHost || !localPort || localPort < 1 || localPort > 65535) {
    notice(ignored, label, 'Ignored proxy because its local endpoint is incomplete or invalid')
    return []
  }
  unsupportedFields(proxy, label, ignored)
  const importedTransport = transport(proxy.transport, label, ignored)
  const importedHealthCheck = healthCheck(proxy.healthCheck, label, ignored)
  const importedHttpOptions = tunnelProtocol === 'http' ? httpOptions(proxy, label, ignored) : undefined
  const options: TunnelOptionsInput = {
    ...(importedTransport ? { transport: importedTransport } : {}),
    ...(importedHealthCheck ? { healthCheck: importedHealthCheck } : {}),
    ...(importedHttpOptions ? { http: importedHttpOptions } : {}),
  }

  if (tunnelProtocol !== 'http') {
    const serverPort = integer(proxy.remotePort)
    if (!serverPort || serverPort < 1 || serverPort > 65535) {
      notice(ignored, label, 'Ignored proxy because its remote port is missing or invalid')
      return []
    }
    return [{ id: `proxy-${index}`, input: { label, protocol: tunnelProtocol, localHost, localPort, serverPort, enabled: false, ...(Object.keys(options).length ? { options } : {}) } }]
  }

  const customDomains = proxy.customDomains
  if (!Array.isArray(customDomains) || !customDomains.length || !customDomains.every(value => typeof value === 'string')) {
    notice(ignored, label, 'Ignored HTTP proxy because it has no custom domains')
    return []
  }
  const locations = proxy.locations === undefined
    ? [null]
    : Array.isArray(proxy.locations) && proxy.locations.every(value => typeof value === 'string')
      ? (proxy.locations.length ? proxy.locations : [null])
      : undefined
  if (!locations || locations.some(location => location !== null && (!location.startsWith('/') || /\s/.test(location)))) {
    notice(ignored, label, 'Ignored HTTP proxy because its locations are invalid')
    return []
  }
  return locations.map((location, locationIndex) => ({
    id: `proxy-${index}-location-${locationIndex}`,
    input: {
      label,
      protocol: 'http',
      customDomains,
      location,
      localHost,
      localPort,
      enabled: false,
      ...(Object.keys(options).length ? { options } : {}),
    },
  }))
}

export function parseFrpcTunnelImport(source: string): FrpcTunnelImport {
  let document: Record<string, unknown>
  try {
    document = tomlCodec.parse(source)
  }
  catch {
    throw new TunnelError('INVALID_CONFIG', 'Tunnel configuration must be valid TOML')
  }
  if (!Array.isArray(document.proxies))
    throw new TunnelError('INVALID_CONFIG', 'Tunnel configuration must contain a proxies array')

  const ignored: TunnelImportNotice[] = []
  const clientSettings = ['serverAddr', 'serverPort', 'user', 'loginFailExit', 'auth', 'log'].filter(key => document[key] !== undefined)
  if (clientSettings.length)
    ignored.push({ reason: 'Client connection settings are not imported' })
  const candidates = document.proxies.flatMap((proxy, index) => {
    const value = table(proxy)
    if (!value) {
      ignored.push({ proxy: `Proxy ${index + 1}`, reason: 'Ignored proxy because it is not a TOML table' })
      return []
    }
    return proxyCandidates(value, index, ignored)
  })
  return { candidates, ignored }
}

export function tunnelImportPreview(imported: FrpcTunnelImport): TunnelImportPreview {
  return {
    candidates: imported.candidates.map(({ id, input }) => ({
      id,
      label: input.label ?? '',
      protocol: input.protocol,
      ...(input.protocol === 'http'
        ? { customDomains: input.customDomains!, location: input.location ?? null }
        : { serverPort: input.serverPort! }),
      localHost: input.localHost ?? '127.0.0.1',
      localPort: input.localPort,
      basicAuth: input.options?.http?.basicAuth
        ? { username: input.options.http.basicAuth.username, passwordConfigured: true }
        : null,
    })),
    ignored: imported.ignored,
  }
}

export function selectedImportedTunnels(imported: FrpcTunnelImport, candidateIds: string[]): TunnelMutationInput[] {
  if (!candidateIds.length)
    throw new TunnelError('INVALID_TUNNEL', 'Select at least one tunnel configuration to import')
  const selected = new Set(candidateIds)
  if (selected.size !== candidateIds.length)
    throw new TunnelError('INVALID_TUNNEL', 'Tunnel configuration selection contains duplicates')
  const candidates = new Map(imported.candidates.map(candidate => [candidate.id, candidate]))
  const inputs = candidateIds.map((id) => {
    const candidate = candidates.get(id)
    if (!candidate)
      throw new TunnelError('INVALID_TUNNEL', 'Tunnel configuration selection is no longer valid')
    return { ...candidate.input, enabled: false }
  })
  return inputs
}
