import type { TunnelView } from './api'
import type { TunnelHeader } from './domain'
import { z } from 'zod'

const valueRowSchema = z.object({ value: z.string() })
const keyValueRowSchema = z.object({ name: z.string(), value: z.string() })

export type TunnelEditorSection = 'basics' | 'transport' | 'health' | 'http'

const baseTunnelSchema = z.object({
  label: z.string().max(100, 'Display name must be 100 characters or fewer'),
  protocol: z.enum(['http', 'tcp', 'udp']),
  customDomains: z.array(valueRowSchema),
  location: z.string(),
  serverPort: z.string(),
  localHost: z.string(),
  localPort: z.string(),
  enabled: z.boolean(),
  useEncryption: z.boolean(),
  useCompression: z.boolean(),
  bandwidthEnabled: z.boolean(),
  bandwidthValue: z.string(),
  bandwidthUnit: z.enum(['KB', 'MB']),
  bandwidthMode: z.enum(['client', 'server']),
  proxyProtocolVersion: z.enum(['', 'v1', 'v2']),
  healthEnabled: z.boolean(),
  healthType: z.enum(['tcp', 'http']),
  healthInterval: z.string(),
  healthTimeout: z.string(),
  healthMaxFailed: z.string(),
  healthPath: z.string(),
  healthHeaders: z.array(keyValueRowSchema),
  authEnabled: z.boolean(),
  authUsername: z.string(),
  authPassword: z.string(),
  hostHeaderRewrite: z.string(),
  requestHeaders: z.array(keyValueRowSchema),
  responseHeaders: z.array(keyValueRowSchema),
})

export type TunnelFormValues = z.infer<typeof baseTunnelSchema>

function positiveInteger(value: string): boolean {
  const number = Number(value)
  return Number.isSafeInteger(number) && number >= 1
}

export function createTunnelSchema({ hasExistingBasicAuth = false }: { hasExistingBasicAuth?: boolean } = {}) {
  return baseTunnelSchema.superRefine((values, context) => {
    const localPort = Number(values.localPort)
    if (!values.localHost.trim())
      context.addIssue({ code: 'custom', path: ['localHost'], message: 'Local host is required' })
    if (!Number.isSafeInteger(localPort) || localPort < 1 || localPort > 65535)
      context.addIssue({ code: 'custom', path: ['localPort'], message: 'Local port must be between 1 and 65535' })

    if (values.protocol === 'http') {
      if (!values.customDomains.some(row => row.value.trim()))
        context.addIssue({ code: 'custom', path: ['customDomains'], message: 'At least one custom domain is required' })
      if (values.location && (!values.location.startsWith('/') || /\s/.test(values.location)))
        context.addIssue({ code: 'custom', path: ['location'], message: 'Location must begin with / and contain no spaces' })
    }
    else if (values.serverPort) {
      const serverPort = Number(values.serverPort)
      if (!Number.isSafeInteger(serverPort) || serverPort < 1 || serverPort > 65535)
        context.addIssue({ code: 'custom', path: ['serverPort'], message: 'Server port must be between 1 and 65535' })
    }

    if (values.bandwidthEnabled && (!Number.isFinite(Number(values.bandwidthValue)) || Number(values.bandwidthValue) <= 0))
      context.addIssue({ code: 'custom', path: ['bandwidthValue'], message: 'Bandwidth limit must be greater than zero' })

    if (values.healthEnabled) {
      if (!positiveInteger(values.healthInterval))
        context.addIssue({ code: 'custom', path: ['healthInterval'], message: 'Health check timing values must be positive integers' })
      if (!positiveInteger(values.healthTimeout))
        context.addIssue({ code: 'custom', path: ['healthTimeout'], message: 'Health check timing values must be positive integers' })
      if (!positiveInteger(values.healthMaxFailed))
        context.addIssue({ code: 'custom', path: ['healthMaxFailed'], message: 'Health check timing values must be positive integers' })
      if (values.healthType === 'http' && (!values.healthPath.startsWith('/') || /\s/.test(values.healthPath)))
        context.addIssue({ code: 'custom', path: ['healthPath'], message: 'Health path must begin with / and contain no spaces' })
    }

    if (values.protocol === 'http' && values.authEnabled) {
      if (!values.authUsername.trim())
        context.addIssue({ code: 'custom', path: ['authUsername'], message: 'Basic Auth username is required' })
      if (!values.authPassword && !hasExistingBasicAuth)
        context.addIssue({ code: 'custom', path: ['authPassword'], message: 'Basic Auth password is required' })
    }
  })
}

function keyValueRows(headers: TunnelHeader[] | undefined): TunnelFormValues['healthHeaders'] {
  return (headers ?? []).map(header => ({ name: header.name, value: header.value }))
}

function headerValues(rows: TunnelFormValues['healthHeaders']): TunnelHeader[] {
  return rows.map(row => ({ name: row.name.trim(), value: row.value.trim() })).filter(row => row.name)
}

export function draftToTunnelForm(initial?: TunnelView): TunnelFormValues {
  const httpTunnel = initial?.protocol === 'http' ? initial : undefined
  const transport = initial?.options.transport
  const health = initial?.options.healthCheck
  const http = httpTunnel?.options.http
  return {
    label: initial?.label ?? '',
    protocol: initial?.protocol ?? 'http',
    customDomains: httpTunnel ? httpTunnel.customDomains.map(value => ({ value })) : [{ value: '' }],
    location: httpTunnel?.location ?? '',
    serverPort: initial?.serverPort?.toString() ?? '',
    localHost: initial?.localHost ?? '127.0.0.1',
    localPort: initial?.localPort.toString() ?? '',
    enabled: initial?.enabled ?? true,
    useEncryption: transport?.useEncryption ?? false,
    useCompression: transport?.useCompression ?? false,
    bandwidthEnabled: Boolean(transport?.bandwidthLimit),
    bandwidthValue: transport?.bandwidthLimit?.value.toString() ?? '1',
    bandwidthUnit: transport?.bandwidthLimit?.unit ?? 'MB',
    bandwidthMode: transport?.bandwidthLimit?.mode ?? 'client',
    proxyProtocolVersion: transport?.proxyProtocolVersion ?? '',
    healthEnabled: Boolean(health),
    healthType: health?.type ?? 'tcp',
    healthInterval: health?.intervalSeconds.toString() ?? '10',
    healthTimeout: health?.timeoutSeconds.toString() ?? '3',
    healthMaxFailed: health?.maxFailed.toString() ?? '3',
    healthPath: health?.type === 'http' ? health.path : '/health',
    healthHeaders: health?.type === 'http' ? keyValueRows(health.headers) : [],
    authEnabled: Boolean(http?.basicAuth),
    authUsername: http?.basicAuth?.username ?? '',
    authPassword: '',
    hostHeaderRewrite: http?.hostHeaderRewrite ?? '',
    requestHeaders: keyValueRows(http?.requestHeaders),
    responseHeaders: keyValueRows(http?.responseHeaders),
  }
}

export function buildTunnelPayload(values: TunnelFormValues): {
  label: string
  protocol: TunnelFormValues['protocol']
  customDomains?: string[]
  location: string | null
  serverPort: number | null
  localHost: string
  localPort: number
  enabled: boolean
  options: {
    transport: {
      useEncryption: boolean
      useCompression: boolean
      bandwidthLimit: { value: number, unit: 'KB' | 'MB', mode: 'client' | 'server' } | null
      proxyProtocolVersion: 'v1' | 'v2' | null
    }
    healthCheck: { type: 'tcp', intervalSeconds: number, timeoutSeconds: number, maxFailed: number } | { type: 'http', path: string, intervalSeconds: number, timeoutSeconds: number, maxFailed: number, headers: TunnelHeader[] } | null
    http: { basicAuth: { username: string, password?: string } | null, hostHeaderRewrite: string | null, requestHeaders: TunnelHeader[], responseHeaders: TunnelHeader[] } | null
  }
} {
  const healthCheck = values.healthEnabled
    ? values.healthType === 'http'
      ? { type: 'http' as const, path: values.healthPath, intervalSeconds: Number(values.healthInterval), timeoutSeconds: Number(values.healthTimeout), maxFailed: Number(values.healthMaxFailed), headers: headerValues(values.healthHeaders) }
      : { type: 'tcp' as const, intervalSeconds: Number(values.healthInterval), timeoutSeconds: Number(values.healthTimeout), maxFailed: Number(values.healthMaxFailed) }
    : null
  const basicAuth = values.authEnabled
    ? { username: values.authUsername, ...(values.authPassword ? { password: values.authPassword } : {}) }
    : null

  return {
    label: values.label,
    protocol: values.protocol,
    customDomains: values.protocol === 'http' ? values.customDomains.map(row => row.value.trim()).filter(Boolean) : undefined,
    location: values.protocol === 'http' ? values.location.trim() || null : null,
    serverPort: values.protocol === 'http' || !values.serverPort ? null : Number(values.serverPort),
    localHost: values.localHost,
    localPort: Number(values.localPort),
    enabled: values.enabled,
    options: {
      transport: {
        useEncryption: values.useEncryption,
        useCompression: values.useCompression,
        bandwidthLimit: values.bandwidthEnabled ? { value: Number(values.bandwidthValue), unit: values.bandwidthUnit, mode: values.bandwidthMode } : null,
        proxyProtocolVersion: values.proxyProtocolVersion || null,
      },
      healthCheck,
      http: values.protocol === 'http'
        ? {
            basicAuth,
            hostHeaderRewrite: values.hostHeaderRewrite.trim() || null,
            requestHeaders: headerValues(values.requestHeaders),
            responseHeaders: headerValues(values.responseHeaders),
          }
        : null,
    },
  }
}

export function sectionForTunnelField(field: string): TunnelEditorSection {
  if (['useEncryption', 'useCompression', 'bandwidthEnabled', 'bandwidthValue', 'bandwidthUnit', 'bandwidthMode', 'proxyProtocolVersion'].includes(field))
    return 'transport'
  if (['healthEnabled', 'healthType', 'healthInterval', 'healthTimeout', 'healthMaxFailed', 'healthPath', 'healthHeaders'].includes(field))
    return 'health'
  if (['authEnabled', 'authUsername', 'authPassword', 'hostHeaderRewrite', 'requestHeaders', 'responseHeaders'].includes(field))
    return 'http'
  return 'basics'
}
