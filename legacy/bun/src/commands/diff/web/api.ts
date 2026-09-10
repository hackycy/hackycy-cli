export type ComparisonStatus = 'added' | 'deleted' | 'modified' | 'unchanged' | 'issue'
export type EntryKind = 'file' | 'symlink' | 'issue'
export type ComparisonSide = 'baseline' | 'target'

export interface StatusCounts {
  added: number
  deleted: number
  modified: number
  unchanged: number
}

export interface Summary {
  id: string
  baselineDirectory: string
  targetDirectory: string
  createdAt: string
  counts: StatusCounts
  issues: number
}

export interface ServerState {
  version: number
  workspace: {
    phase: string
    snapshotId?: string
    error?: string
    progress?: {
      discoveredEntries: number
      comparedEntries: number
      totalEntries?: number
      comparedBytes: number
      totalBytes?: number
      issues: number
    }
  }
  snapshot?: Summary
}

export interface Entry {
  id: number
  path: string
  status: ComparisonStatus
  kind: EntryKind
  baselineSize?: number
  targetSize?: number
  message?: string
}

export interface EntryDetail extends Entry {
  presentation: 'text' | 'image' | 'binary' | 'symlink' | 'oversized' | 'stale' | 'issue'
  baselineLinkTarget?: string
  targetLinkTarget?: string
}

export type TextContent
  = | { status: 'ready', text: string, encoding: string, size: number, lineCount: number }
    | { status: 'guarded', size: number, lineCount: number }
    | { status: 'blocked', size: number, lineCount?: number }
    | { status: 'binary' | 'missing' | 'stale' }

export interface DirectoryNode {
  kind: 'directory'
  name: string
  path: string
  counts: StatusCounts
  issues: number
}

export interface FileNode {
  kind: EntryKind
  name: string
  path: string
  id: number
  status: ComparisonStatus
}

export type TreeNode = DirectoryNode | FileNode

export interface SearchPage {
  results: TreeNode[]
  truncated: boolean
}

export async function apiJson<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init)
  const body = await response.json()
  if (!response.ok)
    throw new Error(body.error?.message ?? `Request failed (${response.status})`)
  return body as T
}

export function contentUrl(snapshotId: string, entryId: number, side: ComparisonSide, force = false): string {
  return `/api/entries/${entryId}/content/${side}?snapshot=${encodeURIComponent(snapshotId)}${force ? '&force=true' : ''}`
}

export function blobUrl(snapshotId: string, entryId: number, side: ComparisonSide): string {
  return `/api/entries/${entryId}/blob/${side}?snapshot=${encodeURIComponent(snapshotId)}`
}
