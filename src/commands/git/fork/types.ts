export interface InstanceConfig {
  host: string
  type: 'github' | 'gitlab'
  token: string // encrypted
}

export interface ForkConfig {
  salt: string
  instances: Record<string, InstanceConfig>
}

export interface ParsedRepo {
  host: string
  owner: string
  repo: string
  ref?: string
  instanceName?: string
}

export interface Provider {
  type: 'github' | 'gitlab'
  getArchiveUrl: (host: string, owner: string, repo: string, ref: string) => string
  getDefaultBranch: (host: string, owner: string, repo: string, token?: string) => Promise<string>
  buildCloneUrl: (host: string, owner: string, repo: string, token?: string) => string
  buildArchiveHeaders: (token?: string) => Record<string, string>
}
