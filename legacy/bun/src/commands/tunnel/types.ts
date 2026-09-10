export const TUNNEL_PROTOCOL_VERSION = 3 as const

export type TunnelProtocol = 'http' | 'tcp' | 'udp'
export type AccountRole = 'admin' | 'user'
export type AccountKind = 'environment' | 'local'
export type ClientConnectionState = 'connected' | 'disconnected' | 'incompatible' | 'revocation_pending'
export type FrpProcessState = 'stopped' | 'running' | 'recovering' | 'configuration_failed'
export type TunnelPresentationState = 'Disabled' | 'Pending' | 'Applied' | 'Error'
export type TunnelErrorCode
  = | 'ACTIVATION_FAILED'
    | 'AUTHENTICATION_FAILED'
    | 'CLIENT_STOPPED'
    | 'CONFIGURATION_FAILED'
    | 'ACCOUNT_NOT_EMPTY'
    | 'AUTHENTICATION_REQUIRED'
    | 'CLIENT_OFFLINE'
    | 'FORBIDDEN'
    | 'FRP_INSTALL_FAILED'
    | 'FRPS_UNAVAILABLE'
    | 'INCOMPATIBLE_CLIENT'
    | 'INSTANCE_ACTIVE'
    | 'INVALID_CLIENT_REMARK'
    | 'INVALID_CUSTOM_404_PAGE'
    | 'INVALID_ACCOUNT'
    | 'INVALID_CURRENT_PASSWORD'
    | 'INVALID_CONFIG'
    | 'INVALID_FRP_ARCHIVE'
    | 'INVALID_FRP_BINARY'
    | 'INVALID_FRP_VERSION'
    | 'INVALID_HOSTNAME'
    | 'INVALID_HTTP_ROUTE'
    | 'INVALID_LOCAL_ENDPOINT'
    | 'INVALID_PROTOCOL'
    | 'INVALID_REVISION'
    | 'INVALID_TUNNEL'
    | 'LOCK_UNAVAILABLE'
    | 'MANAGED_ACCOUNT'
    | 'NOT_FOUND'
    | 'PORT_OUTSIDE_POOL'
    | 'PORT_POOL_EXHAUSTED'
    | 'RESOURCE_RESERVED'
    | 'SESSION_UNAVAILABLE'
    | 'UNSUPPORTED_PLATFORM'
    | 'USERNAME_TAKEN'

export interface TunnelHeader {
  name: string
  value: string
}

export interface TunnelBandwidthLimit {
  value: number
  unit: 'KB' | 'MB'
  mode: 'client' | 'server'
}

export interface TunnelTransportOptions {
  useEncryption: boolean
  useCompression: boolean
  bandwidthLimit: TunnelBandwidthLimit | null
  proxyProtocolVersion: 'v1' | 'v2' | null
}

export interface TunnelTcpHealthCheck {
  type: 'tcp'
  intervalSeconds: number
  timeoutSeconds: number
  maxFailed: number
}

export interface TunnelHttpHealthCheck {
  type: 'http'
  path: string
  intervalSeconds: number
  timeoutSeconds: number
  maxFailed: number
  headers: TunnelHeader[]
}

export type TunnelHealthCheck = TunnelTcpHealthCheck | TunnelHttpHealthCheck

export interface TunnelHttpOptions {
  basicAuth: { username: string, password: string } | null
  hostHeaderRewrite: string | null
  requestHeaders: TunnelHeader[]
  responseHeaders: TunnelHeader[]
}

export interface TunnelOptions {
  transport: TunnelTransportOptions
  healthCheck: TunnelHealthCheck | null
  http: TunnelHttpOptions | null
}

interface TunnelDefinitionBase {
  id: string
  label: string
  protocol: TunnelProtocol
  localHost: string
  localPort: number
  enabled: boolean
  options: TunnelOptions
  createdAt: string
  updatedAt: string
}

export interface HttpTunnelDefinition extends TunnelDefinitionBase {
  protocol: 'http'
  customDomains: string[]
  location: string | null
  serverPort: null
}

export interface PortTunnelDefinition extends TunnelDefinitionBase {
  protocol: 'tcp' | 'udp'
  serverPort: number
}

export type TunnelDefinition = HttpTunnelDefinition | PortTunnelDefinition

export interface PublicTunnelHttpOptions extends Omit<TunnelHttpOptions, 'basicAuth'> {
  basicAuth: { username: string, passwordConfigured: true } | null
}

export interface PublicTunnelOptions extends Omit<TunnelOptions, 'http'> {
  http: PublicTunnelHttpOptions | null
}

type RedactedTunnel<T> = T extends TunnelDefinition ? Omit<T, 'options'> & { options: PublicTunnelOptions } : never
export type PublicTunnelDefinition = RedactedTunnel<TunnelDefinition>

export interface TunnelSnapshot {
  clientKey: string
  revision: number
  tunnels: TunnelDefinition[]
}

export interface FrpcDesiredConfiguration {
  advertisedFrpHost: string
  advertisedFrpPort: number
  internalFrpToken: string
  snapshot: TunnelSnapshot
}

export interface StructuredRuntimeError {
  code: string
  message: string
  revision?: number
}

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

export interface AccountRecord {
  id: string
  kind: AccountKind
  username: string
  role: AccountRole
  createdAt: string
  updatedAt: string
}

export interface ClientRuntimeState {
  connectionState: ClientConnectionState
  processState: FrpProcessState
  lastError?: StructuredRuntimeError
}

export interface FrpArtifactDescription {
  version: string
  archive: string
  url: string
  sha256: string
  frpcSha256: string
}

export interface AgentHelloMessage {
  type: 'hello'
  tunnelProtocolVersion: number
  ycyVersion: string
  platform: string
  architecture: string
  lastAppliedRevision: number
}

export interface AgentWelcomeMessage {
  type: 'welcome'
  tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION
  requiredFrpVersion: string
  artifact: FrpArtifactDescription
  advertisedFrpHost: string
  advertisedFrpPort: number
  internalFrpToken: string
  snapshot: TunnelSnapshot
}

export interface DesiredStateMessage {
  type: 'desired_state'
  tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION
  snapshot: TunnelSnapshot
}

export interface ApplyResultMessage {
  type: 'apply_result'
  tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION
  revision: number
  success: boolean
  error?: StructuredRuntimeError
}

export interface ProcessStateMessage {
  type: 'process_state'
  tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION
  state: FrpProcessState
  error?: StructuredRuntimeError
}

export type AgentToServerMessage = AgentHelloMessage | ApplyResultMessage | ProcessStateMessage

export type ServerToAgentMessage
  = | AgentWelcomeMessage
    | DesiredStateMessage
    | { type: 'restart_frpc', tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION }
    | { type: 'revoke', tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION, reason: 'rotated' | 'deleted' }
    | { type: 'incompatible', tunnelProtocolVersion: typeof TUNNEL_PROTOCOL_VERSION, message: string }

export interface ServerTunnelConfig {
  address: string
  controlPort: number
  frpPort: number
  httpPort: number
  portRange: { start: number, end: number }
  advertiseFrpAddress?: { host: string, port: number }
  frpToken?: string
  dataDir: string
  adminUser: string
  adminPassword: string
  sessionIdleLifetimeMs?: number
}

export interface ClientTunnelConfig {
  server: URL
  token: string
  stateDir: string
}

export class TunnelError extends Error {
  constructor(public readonly code: TunnelErrorCode, message: string) {
    super(message)
    this.name = 'TunnelError'
  }
}
