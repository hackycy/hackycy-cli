export interface CmProfile {
  baseURL: string
  model: string
  apiKey: string // encrypted
  temperature?: number
  timeoutMs?: number
  maxOutputTokens?: number
}

export interface CmConfig {
  defaultProfile?: string
  profiles: Record<string, CmProfile>
}

export interface ResolvedCmProfile {
  name: string
  baseURL: string
  model: string
  apiKey: string
  temperature: number
  timeoutMs: number
  maxOutputTokens: number
}

export interface ForkInstanceConfig {
  host: string
  scheme?: 'http' | 'https' // default 'https'
  type: 'github' | 'gitlab'
  token: string // encrypted
}

export interface ForkConfig {
  instances: Record<string, ForkInstanceConfig>
}

export interface StoredTunnelConnection {
  server: string
  token: string // encrypted
  lastAuthenticatedAt: string
}

export interface TunnelConfig {
  connections: Record<string, StoredTunnelConnection>
}

export interface AppConfig {
  salt: string
  fork: ForkConfig
  cm?: CmConfig
  tunnel?: TunnelConfig
}
