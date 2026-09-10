import type { ChatCompletionTokenUsage } from '../../../config/client'

export type CommitLanguage = 'en' | 'zh'

export type GitScope = 'all-uncommitted' | 'staged'

export type FileRole
  = | 'source'
    | 'test'
    | 'docs'
    | 'config'
    | 'dependency'
    | 'generated'
    | 'binary'
    | 'sensitive'
    | 'unknown'

export type ContentPolicy = 'inspect' | 'metadata-only' | 'redacted'

export interface CmOptions {
  profile?: string
  timeoutMs?: number
  lang?: CommitLanguage
  staged?: boolean
  stage?: boolean
  stageAll?: boolean
  push?: boolean | string
  stagePush?: boolean | string
  dryRun?: boolean
  body?: boolean
}

export interface ChangeStats {
  additions: number
  deletions: number
}

export interface DiffHunk {
  id: string
  source: 'staged' | 'worktree' | 'untracked'
  oldStart: number
  oldLines: number
  newStart: number
  newLines: number
  heading?: string
  addedLines: string[]
  deletedLines: string[]
}

export interface SnapshotFile {
  id: string
  path: string
  originalPath?: string
  status: string
  indexStatus: string
  worktreeStatus: string
  role: FileRole
  contentPolicy: ContentPolicy
  stats: ChangeStats
  hunks: DiffHunk[]
  manifest?: {
    before?: string
    after?: string
  }
}

export interface GitChangeSnapshot {
  repoRoot: string
  scope: GitScope
  snapshotId: string
  files: SnapshotFile[]
  totals: ChangeStats
}

export interface FileChange {
  path: string
  originalPath?: string
  status: string
  indexStatus: string
  worktreeStatus: string
}

export interface EvidenceFact {
  id: string
  priority: 1 | 2 | 3
  clusterKey: string
  filePath: string
  hunkId?: string
  text: string
}

export interface EvidenceCoverage {
  estimatedLocalPromptTokens: number
  representedClusters: number
  totalClusters: number
  includedFacts: number
  omittedFacts: number
  contentCompacted: boolean
}

export interface GenerateCommitMessageInput {
  repoRoot: string
  scope: GitScope
  language: CommitLanguage
  includeBody: boolean
}

export interface GeneratedCommitMessage {
  message: string
  snapshotId: string
  fileCount: number
  usage?: ChatCompletionTokenUsage
  evidence: EvidenceCoverage
}

export interface CommitMessageEngine {
  generate: (input: GenerateCommitMessageInput) => Promise<GeneratedCommitMessage>
}

export type CommitMessageErrorCode
  = | 'NO_CHANGES'
    | 'GIT_CAPTURE_FAILED'
    | 'EVIDENCE_BUILD_FAILED'
    | 'MODEL_UNAVAILABLE'
    | 'INVALID_MODEL_OUTPUT'
    | 'STALE_GIT_SCOPE'

export class CommitMessageError extends Error {
  constructor(
    readonly code: CommitMessageErrorCode,
    message: string,
    override readonly cause?: unknown,
  ) {
    super(message)
    this.name = 'CommitMessageError'
  }
}

export function isCommitMessageError(error: unknown, code?: CommitMessageErrorCode): error is CommitMessageError {
  return error instanceof CommitMessageError && (code === undefined || error.code === code)
}
