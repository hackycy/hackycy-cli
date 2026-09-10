import type {
  PublicTunnelDefinition,
  TunnelBandwidthLimit,
  TunnelDefinition,
  TunnelHeader,
  TunnelHealthCheck,
  TunnelHttpOptions,
  TunnelOptions,
  TunnelProtocol,
  TunnelTransportOptions,
} from './types'
import { TunnelError } from './types'

export interface TunnelTransportOptionsInput {
  useEncryption?: boolean
  useCompression?: boolean
  bandwidthLimit?: Omit<TunnelBandwidthLimit, never> | null
  proxyProtocolVersion?: 'v1' | 'v2' | null
}

export type TunnelHealthCheckInput
  = | { type: 'tcp', intervalSeconds: number, timeoutSeconds: number, maxFailed: number }
    | { type: 'http', path: string, intervalSeconds: number, timeoutSeconds: number, maxFailed: number, headers?: TunnelHeader[] }

export interface TunnelHttpOptionsInput {
  basicAuth?: { username: string, password?: string } | null
  hostHeaderRewrite?: string | null
  requestHeaders?: TunnelHeader[]
  responseHeaders?: TunnelHeader[]
}

export interface TunnelOptionsInput {
  transport?: TunnelTransportOptionsInput
  healthCheck?: TunnelHealthCheckInput | null
  http?: TunnelHttpOptionsInput | null
}

const DEFAULT_TRANSPORT: TunnelTransportOptions = {
  useEncryption: false,
  useCompression: false,
  bandwidthLimit: null,
  proxyProtocolVersion: null,
}

const DEFAULT_HTTP: TunnelHttpOptions = {
  basicAuth: null,
  hostHeaderRewrite: null,
  requestHeaders: [],
  responseHeaders: [],
}

function positiveInteger(value: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < 1)
    throw new TunnelError('INVALID_TUNNEL', `${label} must be a positive integer`)
  return value
}

function normalizeHeaders(input: TunnelHeader[] | undefined, label: string): TunnelHeader[] {
  if (!input)
    return []
  if (input.length > 32)
    throw new TunnelError('INVALID_TUNNEL', `${label} accepts at most 32 headers`)
  const seen = new Set<string>()
  return input.map((entry) => {
    const name = entry.name.trim()
    const value = entry.value.trim()
    if (!name || name.length > 128 || !/^[!#$%&'*+.^`|~\w-]+$/.test(name))
      throw new TunnelError('INVALID_TUNNEL', `${label} contains an invalid header name`)
    if (value.length > 4096 || /[\r\n]/.test(value))
      throw new TunnelError('INVALID_TUNNEL', `${label} contains an invalid header value`)
    const key = name.toLowerCase()
    if (seen.has(key))
      throw new TunnelError('INVALID_TUNNEL', `${label} contains a duplicate header name`)
    seen.add(key)
    return { name, value }
  })
}

function normalizeBandwidth(input: TunnelBandwidthLimit | null | undefined, current: TunnelBandwidthLimit | null): TunnelBandwidthLimit | null {
  if (input === undefined)
    return current
  if (input === null)
    return null
  if (!Number.isFinite(input.value) || input.value <= 0)
    throw new TunnelError('INVALID_TUNNEL', 'Bandwidth limit must be greater than zero')
  if (!['KB', 'MB'].includes(input.unit) || !['client', 'server'].includes(input.mode))
    throw new TunnelError('INVALID_TUNNEL', 'Bandwidth limit unit or mode is invalid')
  return { value: input.value, unit: input.unit, mode: input.mode }
}

function normalizeHealthCheck(input: TunnelHealthCheckInput | null | undefined, current: TunnelHealthCheck | null): TunnelHealthCheck | null {
  if (input === undefined)
    return current
  if (input === null)
    return null
  const base = {
    intervalSeconds: positiveInteger(input.intervalSeconds, 'Health check interval'),
    timeoutSeconds: positiveInteger(input.timeoutSeconds, 'Health check timeout'),
    maxFailed: positiveInteger(input.maxFailed, 'Health check failure threshold'),
  }
  if (input.type === 'tcp')
    return { type: 'tcp', ...base }
  const path = input.path.trim()
  if (!path.startsWith('/') || /\s/.test(path))
    throw new TunnelError('INVALID_TUNNEL', 'HTTP health check path must begin with / and contain no spaces')
  return { type: 'http', path, ...base, headers: normalizeHeaders(input.headers, 'Health check headers') }
}

function normalizeBasicAuth(
  input: TunnelHttpOptionsInput['basicAuth'],
  current: TunnelHttpOptions['basicAuth'],
): TunnelHttpOptions['basicAuth'] {
  if (input === undefined)
    return current
  if (input === null)
    return null
  const username = input.username.trim()
  const password = input.password ?? current?.password
  if (!username || username.length > 256 || password === undefined || password.length < 1 || password.length > 256)
    throw new TunnelError('INVALID_TUNNEL', 'HTTP Basic Auth requires a username and password of at most 256 characters')
  return { username, password }
}

function normalizeHostHeader(input: string | null | undefined, current: string | null): string | null {
  if (input === undefined)
    return current
  if (input === null)
    return null
  const value = input.trim()
  if (!value || value.length > 1024 || /[\r\n]/.test(value))
    throw new TunnelError('INVALID_TUNNEL', 'Host Header Rewrite is invalid')
  return value
}

export function normalizeTunnelLabel(input: string | undefined): string {
  const value = input?.trim() ?? ''
  if (value.length > 100)
    throw new TunnelError('INVALID_TUNNEL', 'Tunnel display name must contain no more than 100 characters')
  return value
}

export function normalizeTunnelOptions(
  protocol: TunnelProtocol,
  input: TunnelOptionsInput | undefined,
  current?: TunnelOptions,
): TunnelOptions {
  const previousTransport = current?.transport ?? DEFAULT_TRANSPORT
  const transportInput = input?.transport
  const transport: TunnelTransportOptions = {
    useEncryption: transportInput?.useEncryption ?? previousTransport.useEncryption,
    useCompression: transportInput?.useCompression ?? previousTransport.useCompression,
    bandwidthLimit: normalizeBandwidth(transportInput?.bandwidthLimit, previousTransport.bandwidthLimit),
    proxyProtocolVersion: transportInput?.proxyProtocolVersion === undefined
      ? previousTransport.proxyProtocolVersion
      : transportInput.proxyProtocolVersion,
  }
  const healthCheck = normalizeHealthCheck(input?.healthCheck, current?.healthCheck ?? null)
  if (protocol !== 'http')
    return { transport, healthCheck, http: null }

  const previousHttp = current?.http ?? DEFAULT_HTTP
  const httpInput = input?.http
  if (httpInput === null)
    return { transport, healthCheck, http: { ...DEFAULT_HTTP } }
  const http: TunnelHttpOptions = {
    basicAuth: normalizeBasicAuth(httpInput?.basicAuth, previousHttp.basicAuth),
    hostHeaderRewrite: normalizeHostHeader(httpInput?.hostHeaderRewrite, previousHttp.hostHeaderRewrite),
    requestHeaders: httpInput?.requestHeaders === undefined
      ? previousHttp.requestHeaders.map(header => ({ ...header }))
      : normalizeHeaders(httpInput.requestHeaders, 'Request headers'),
    responseHeaders: httpInput?.responseHeaders === undefined
      ? previousHttp.responseHeaders.map(header => ({ ...header }))
      : normalizeHeaders(httpInput.responseHeaders, 'Response headers'),
  }
  return { transport, healthCheck, http }
}

export function redactTunnelDefinition(tunnel: TunnelDefinition): PublicTunnelDefinition {
  const http = tunnel.options.http
  return {
    ...tunnel,
    options: {
      transport: tunnel.options.transport,
      healthCheck: tunnel.options.healthCheck,
      http: http
        ? {
            ...http,
            basicAuth: http.basicAuth
              ? { username: http.basicAuth.username, passwordConfigured: true }
              : null,
          }
        : null,
    },
  } as PublicTunnelDefinition
}
