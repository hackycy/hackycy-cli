export type AccountRole = 'admin' | 'user'
export type AccountKind = 'environment' | 'local'
export type ClientConnectionState = 'connected' | 'disconnected' | 'incompatible' | 'revocation_pending'
export type FrpProcessState = 'stopped' | 'running' | 'recovering' | 'configuration_failed'
export type TunnelPresentationState = 'Disabled' | 'Pending' | 'Applied' | 'Error'
export type TunnelProtocol = 'http' | 'tcp' | 'udp'

export interface TunnelHeader {
  name: string
  value: string
}

export interface PublicTunnelOptions {
  transport: {
    useEncryption: boolean
    useCompression: boolean
    bandwidthLimit: { value: number, unit: 'KB' | 'MB', mode: 'client' | 'server' } | null
    proxyProtocolVersion: 'v1' | 'v2' | null
  }
  healthCheck: ({ type: 'tcp', intervalSeconds: number, timeoutSeconds: number, maxFailed: number } | { type: 'http', path: string, intervalSeconds: number, timeoutSeconds: number, maxFailed: number, headers: TunnelHeader[] }) | null
  http: {
    basicAuth: { username: string, passwordConfigured: true } | null
    hostHeaderRewrite: string | null
    requestHeaders: TunnelHeader[]
    responseHeaders: TunnelHeader[]
  } | null
}

interface TunnelDefinitionBase {
  id: string
  label: string
  protocol: TunnelProtocol
  localHost: string
  localPort: number
  enabled: boolean
  options: PublicTunnelOptions
  createdAt: string
  updatedAt: string
}

export type PublicTunnelDefinition = TunnelDefinitionBase & ({
  protocol: 'http'
  customDomains: string[]
  location: string | null
  serverPort: null
} | {
  protocol: 'tcp' | 'udp'
  serverPort: number
})

export interface ClientRecord {
  id: string
  ownerAccountId: string
  remark: string
  token: string
  desiredRevision: number
  lastAppliedRevision: number
  revocationPending: boolean
  createdAt: string
  rotatedAt: string | null
}

export interface ClientRuntimeState {
  connectionState: ClientConnectionState
  processState: FrpProcessState
  lastError?: { code: string, message: string, revision?: number }
}
