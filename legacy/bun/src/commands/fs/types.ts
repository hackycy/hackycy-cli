export interface FsOptions {
  directory: string
  port: number
  address: string
  manage: boolean
  safeHtml: boolean
  accounts: string[]
  sessionDir?: string
  sessionIdleDays?: number
  chunkedUpload?: boolean
  uploadChunkSizeMiB?: number
}

export type FsEntryKind = 'directory' | 'file' | 'unavailable'
export type FsPreviewKind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'none'

export interface FsDirectoryEntry {
  name: string
  path: string
  kind: FsEntryKind
  isSymlink: boolean
  size?: number
  modifiedAt?: string
  mimeType?: string
  previewKind: FsPreviewKind
  syntaxLanguage?: string
  browseUrl?: string
  fileUrl?: string
  thumbnailUrl?: string
  downloadUrl?: string
  extractable: boolean
}

export interface FsDirectoryListing {
  rootName: string
  path: string
  parentPath?: string
  entries: FsDirectoryEntry[]
}

export interface FsFile {
  name: string
  size: number
  modifiedAt: Date
  mimeType: string
  body: Blob
}

export type FsTextPreview
  = | { status: 'ready', text: string, encoding: 'utf-8' | 'utf-16le' | 'utf-16be', size: number, revision: string }
    | { status: 'too_large', size: number, maxBytes: number }
    | { status: 'binary', size: number }

export interface FsTextSaveResult {
  revision: string
  size: number
  modifiedAt: Date
  encoding: 'utf-8' | 'utf-16le' | 'utf-16be'
}

export interface FsUploadResult {
  filename: string
  path: string
  size: number
}

export interface FsChunkedUpload {
  writeChunk: (offset: number, stream: ReadableStream<Uint8Array>, expectedLength: number, options?: { signal?: AbortSignal }) => Promise<void>
  publish: () => Promise<FsUploadResult>
  abort: () => Promise<void>
}

export interface FsStreamWriteOptions {
  signal?: AbortSignal
  onProgress?: (bytesWritten: number) => void
}

export interface FsArchiveExtractOptions {
  signal?: AbortSignal
  onProgress?: (progress: number) => void
  onInspect?: (details: { uncompressedBytes: number, entryCount: number }) => void
}

export interface FsArchiveExtractResult {
  archivePath: string
  destinationPath: string
  uncompressedBytes: number
  entryCount: number
}

export type FsExtractionStatus = 'queued' | 'running' | 'done' | 'error' | 'cancelled'

export interface FsExtractionTask {
  id: string
  archivePath: string
  status: FsExtractionStatus
  progress?: number
  uncompressedBytes?: number
  entryCount?: number
  destinationPath?: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
  error?: string
}

export interface FsExtractionManager {
  list: () => FsExtractionTask[]
  enqueue: (paths: string[]) => Promise<FsExtractionTask[]>
  cancel: (id: string) => FsExtractionTask | undefined
  retry: (id: string) => Promise<FsExtractionTask>
  clearTerminal: () => void
  subscribe: (listener: (tasks: FsExtractionTask[]) => void) => () => void
  close: () => Promise<void>
}

export type FsDownloadStatus = 'queued' | 'running' | 'done' | 'error' | 'cancelled'

export interface FsDownloadRequest {
  url: string
  directoryPath: string
  filename?: string
}

export interface FsDownloadTask {
  id: string
  url: string
  directoryPath: string
  filename?: string
  status: FsDownloadStatus
  bytesDownloaded: number
  totalBytes?: number
  progress?: number
  speedBytesPerSecond?: number
  destinationPath?: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
  error?: string
}

export interface FsDownloadManager {
  list: () => FsDownloadTask[]
  enqueue: (request: FsDownloadRequest) => Promise<FsDownloadTask>
  cancel: (id: string) => FsDownloadTask | undefined
  retry: (id: string) => Promise<FsDownloadTask>
  clearTerminal: () => void
  subscribe: (listener: (tasks: FsDownloadTask[]) => void) => () => void
  close: () => Promise<void>
}

export type FsOperation
  = | { action: 'create-directory', parentPath: string, name: string }
    | { action: 'rename', path: string, newName: string }
    | { action: 'copy', paths: string[], destinationPath: string }
    | { action: 'move', paths: string[], destinationPath: string }
    | { action: 'delete', paths: string[] }

export type FsOperationItem
  = | {
    status: 'ok'
    sourcePath?: string
    destinationPath?: string
  }
  | {
    status: 'error'
    sourcePath?: string
    destinationPath?: string
    error: { code: FsErrorCode, message: string }
  }

export interface FsOperationResult {
  action: FsOperation['action']
  items: FsOperationItem[]
}

export interface FsWorkspace {
  listDirectory: (relativePath: string) => Promise<FsDirectoryListing>
  openFile: (relativePath: string) => Promise<FsFile>
  readTextPreview: (relativePath: string) => Promise<FsTextPreview>
  saveTextFile: (relativePath: string, text: string, revision: string) => Promise<FsTextSaveResult>
  uploadFile: (directoryPath: string, file: File) => Promise<FsUploadResult>
  beginChunkedUpload: (directoryPath: string, filename: string, size: number) => Promise<FsChunkedUpload>
  writeFileStream: (directoryPath: string, filename: string, stream: ReadableStream<Uint8Array>, options?: FsStreamWriteOptions) => Promise<FsUploadResult>
  extractArchive: (archivePath: string, options?: FsArchiveExtractOptions) => Promise<FsArchiveExtractResult>
  applyOperation: (operation: FsOperation) => Promise<FsOperationResult>
}

export type FsErrorCode
  = | 'INVALID_PATH'
    | 'INVALID_UPLOAD'
    | 'INVALID_NAME'
    | 'INVALID_OPERATION'
    | 'PATH_FORBIDDEN'
    | 'NOT_FOUND'
    | 'NOT_DIRECTORY'
    | 'NOT_FILE'
    | 'TOO_LARGE'
    | 'PRECONDITION_REQUIRED'
    | 'REVISION_MISMATCH'
    | 'UNSUPPORTED_TEXT'
    | 'NAME_EXHAUSTED'
    | 'ALREADY_EXISTS'
    | 'ROOT_IMMUTABLE'
    | 'UNAVAILABLE'
    | 'UNSUPPORTED_ARCHIVE'
    | 'INVALID_ARCHIVE'
    | 'ENCRYPTED_ARCHIVE'
    | 'INSUFFICIENT_SPACE'

export class FsWorkspaceError extends Error {
  constructor(
    readonly code: FsErrorCode,
    message: string,
    options?: ErrorOptions,
  ) {
    super(message, options)
    this.name = 'FsWorkspaceError'
  }
}
