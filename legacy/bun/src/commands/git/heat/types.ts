export type ChangeKind = 'M' | 'A' | 'D' | 'R' | 'C'
export type HeatTarget = 'files' | 'directories'
export type HeatSort = 'count' | 'path'

export interface GitHeatOptions {
  limit?: number
  days?: number
  type?: HeatTarget
  sort?: HeatSort
  relativeTime?: boolean
  query?: string
}

export interface ChangeCounts {
  total: number
  modified: number
  added: number
  deleted: number
  renamed: number
  copied: number
}

export interface PathHeat extends ChangeCounts {
  path: string
  lastChangedAt: string
  lastChangedAtEpoch: number
}

export interface HeatReport {
  repoRoot: string
  repoName: string
  rangeLabel: string
  target: HeatTarget
  sort: HeatSort
  relativeTime: boolean
  query?: string
  commitCount: number
  files: PathHeat[]
  directories: PathHeat[]
}
