export type ComparisonStatus = 'added' | 'deleted' | 'modified' | 'unchanged'
export type ComparisonResultStatus = ComparisonStatus | 'issue'
export type ComparisonEntryKind = 'file' | 'symlink'
export type ComparisonItemKind = ComparisonEntryKind | 'issue'
export type ComparisonSide = 'baseline' | 'target'
export type WorkspacePhase = 'idle' | 'discovering' | 'comparing' | 'publishing' | 'ready' | 'canceled' | 'error'

export interface StatusCounts {
  added: number
  deleted: number
  modified: number
  unchanged: number
}

export interface SnapshotSummary {
  readonly id: string
  readonly baselineDirectory: string
  readonly targetDirectory: string
  readonly createdAt: string
  readonly counts: Readonly<StatusCounts>
  readonly issues: number
}

export interface ComparisonEntry {
  readonly id: number
  readonly path: string
  readonly status: ComparisonStatus
  readonly baseline?: ComparisonEntryState
  readonly target?: ComparisonEntryState
}

export type ComparisonEntryState
  = | { readonly kind: 'file', readonly size: number }
    | { readonly kind: 'symlink', readonly linkTarget: string }

export interface ComparisonIssue {
  readonly id: number
  readonly path: string
  readonly status: 'issue'
  readonly kind: 'issue'
  readonly message: string
}

export type ComparisonListEntry = ComparisonEntry | ComparisonIssue

export interface EntryQuery {
  includeUnchanged?: boolean
  cursor?: string
  anchor?: number
  limit?: number
  statuses?: ComparisonResultStatus[]
  path?: string
  kinds?: ComparisonItemKind[]
}

export interface EntryPage {
  entries: ComparisonListEntry[]
  nextCursor?: string
}

export interface TreeQuery {
  path: string
}

export interface DirectoryTreeNode {
  kind: 'directory'
  name: string
  path: string
  counts: StatusCounts
  issues: number
}

export interface EntryTreeNode {
  kind: ComparisonItemKind
  name: string
  path: string
  id: number
  status: ComparisonResultStatus
  message?: string
}

export type TreeNode = DirectoryTreeNode | EntryTreeNode

export interface TreePage {
  children: TreeNode[]
}

export interface SearchPage {
  results: TreeNode[]
  truncated: boolean
}

export type EntryPresentation = 'text' | 'image' | 'binary' | 'symlink' | 'oversized' | 'stale' | 'issue'

export type EntryDetail
  = | (ComparisonEntry & {
    presentation: Exclude<EntryPresentation, 'issue'>
  })
  | (ComparisonIssue & { presentation: 'issue' })

export type TextEncoding = 'utf-8' | 'utf-16le' | 'utf-16be'

export type TextContent
  = | { status: 'ready', text: string, encoding: TextEncoding, size: number, lineCount: number }
    | { status: 'guarded', size: number, lineCount: number }
    | { status: 'blocked', size: number, lineCount?: number }
    | { status: 'binary' | 'missing' | 'stale' }

export interface TextDiffOptions {
  contextLines?: number
}

export interface ReadyTextDiff {
  status: 'ready'
  path: string
  comparisonStatus: Exclude<ComparisonStatus, 'unchanged'>
  contextLines: number
  baselineEncoding?: TextEncoding
  targetEncoding?: TextEncoding
  addedLines: number
  deletedLines: number
  patch: string
}

export interface NoTextualChanges {
  status: 'no_textual_changes'
  path: string
  comparisonStatus: 'modified'
  reason: 'encoding_or_bom_only'
  baselineEncoding: TextEncoding
  targetEncoding: TextEncoding
}

export interface UnavailableTextDiff {
  status: 'unavailable'
  path: string
  comparisonStatus: Exclude<ComparisonStatus, 'unchanged'>
  reason: 'non_text' | 'mixed_entry_kinds' | 'source_too_large' | 'stale' | 'complexity_limit' | 'output_too_large' | 'server_busy'
  baselineSize?: number
  baselineLineCount?: number
  targetSize?: number
  targetLineCount?: number
  addedLines?: number
  deletedLines?: number
  outputBytes?: number
}

export type TextDiffResult = ReadyTextDiff | NoTextualChanges | UnavailableTextDiff

export type BlobContent
  = | { status: 'ready', bytes: Uint8Array, mimeType: string, filename: string }
    | { status: 'missing' | 'stale' | 'unavailable' }

export interface WorkspaceProgress {
  discoveredEntries: number
  comparedEntries: number
  totalEntries?: number
  comparedBytes: number
  totalBytes?: number
  issues: number
}

export interface WorkspaceState {
  phase: WorkspacePhase
  snapshotId?: string
  error?: string
  progress?: WorkspaceProgress
}

export interface ComparisonSnapshot {
  readonly summary: SnapshotSummary
  list: (query: EntryQuery) => EntryPage
  tree: (query: TreeQuery) => TreePage
  search: (query: string, statuses?: ComparisonResultStatus[], limit?: number) => SearchPage
  detail: (entryId: number) => Promise<EntryDetail>
  content: (entryId: number, side: ComparisonSide, force?: boolean) => Promise<TextContent>
  textDiff: (entryId: number, options?: TextDiffOptions) => Promise<TextDiffResult>
  blob: (entryId: number, side: ComparisonSide) => Promise<BlobContent>
}

export interface RefreshRun {
  result: Promise<ComparisonSnapshot>
  cancel: () => void
}

export interface ComparisonWorkspace {
  state: () => WorkspaceState
  refresh: () => RefreshRun
  snapshot: (id?: string) => ComparisonSnapshot | undefined
  observe: (listener: (state: WorkspaceState) => void) => () => void
}

export interface ComparisonWorkspaceOptions {
  baselineDirectory: string
  targetDirectory: string
  gitignore?: boolean
  exclusions?: string[]
}
