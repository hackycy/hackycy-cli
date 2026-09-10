export type EntryKind = 'directory' | 'file' | 'unavailable'
export type PreviewKind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'none'

export interface DirectoryEntry {
  name: string
  path: string
  kind: EntryKind
  isSymlink: boolean
  size?: number
  modifiedAt?: string
  mimeType?: string
  previewKind: PreviewKind
  syntaxLanguage?: string
  browseUrl?: string
  fileUrl?: string
  thumbnailUrl?: string
  downloadUrl?: string
  extractable: boolean
}

export interface DirectoryListing {
  version: 1
  rootName: string
  path: string
  parentPath?: string
  managementEnabled: boolean
  maxUploadBytes: number
  chunkedUpload?: {
    thresholdBytes: number
    chunkSizeBytes: number
  }
  entries: DirectoryEntry[]
}

export type SessionState
  = | { version: 1, authenticationEnabled: false }
    | { version: 1, authenticationEnabled: true, authenticated: false }
    | { version: 1, authenticationEnabled: true, authenticated: true, account: { username: string } }

export type TextPreview
  = | { version: 1, status: 'ready', text: string, encoding: 'utf-8' | 'utf-16le' | 'utf-16be', size: number, revision: string }
    | { version: 1, status: 'too_large', size: number, maxBytes: number }
    | { version: 1, status: 'binary', size: number }

export interface TextSaveResult {
  version: 1
  revision: string
  size: number
  modifiedAt: string
  encoding: 'utf-8' | 'utf-16le' | 'utf-16be'
}

export interface UploadResult {
  version: 1
  filename: string
  path: string
  size: number
}

export type ChunkedUpload
  = | { id: string, status: 'uploading', size: number, uploadedBytes: number, chunkSizeBytes: number }
    | { id: string, status: 'complete', size: number, uploadedBytes: number, chunkSizeBytes: number, result: UploadResult }

interface ChunkedUploadResponse {
  version: 1
  upload: ChunkedUpload
}

export type OperationCommand
  = | { action: 'create-directory', parentPath: string, name: string }
    | { action: 'rename', path: string, newName: string }
    | { action: 'copy', paths: string[], destinationPath: string }
    | { action: 'move', paths: string[], destinationPath: string }
    | { action: 'delete', paths: string[] }

export type OperationItem
  = | { status: 'ok', sourcePath?: string, destinationPath?: string }
    | { status: 'error', sourcePath?: string, destinationPath?: string, error: { code: string, message: string } }

export interface OperationResult {
  version: 1
  action: OperationCommand['action']
  items: OperationItem[]
}

export type DownloadStatus = 'queued' | 'running' | 'done' | 'error' | 'cancelled'

export interface DownloadTask {
  id: string
  url: string
  directoryPath: string
  filename?: string
  status: DownloadStatus
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

export interface DownloadList {
  version: 1
  tasks: DownloadTask[]
}

export interface DownloadResponse {
  version: 1
  task: DownloadTask
}

export type ExtractionStatus = 'queued' | 'running' | 'done' | 'error' | 'cancelled'

export interface ExtractionTask {
  id: string
  archivePath: string
  destinationPath?: string
  status: ExtractionStatus
  progress?: number
  uncompressedBytes?: number
  entryCount?: number
  createdAt: string
  startedAt?: string
  finishedAt?: string
  error?: string
}

export interface ExtractionList {
  version: 1
  tasks: ExtractionTask[]
}

export interface ExtractionBatchResponse {
  version: 1
  tasks: ExtractionTask[]
}

export interface ExtractionResponse {
  version: 1
  task: ExtractionTask
}

export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

function abortError(): DOMException {
  return new DOMException('Upload was cancelled', 'AbortError')
}

interface ErrorBody {
  error?: { message?: string }
}

export async function apiJson<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init)
  const body = response.status === 204 ? undefined : await response.json() as T & ErrorBody
  if (!response.ok) {
    if (response.status === 401 && url !== '/api/session')
      window.dispatchEvent(new Event('fs-authentication-required'))
    throw new ApiError(response.status, body?.error?.message ?? `Request failed (${response.status})`)
  }
  return body as T
}

export function applyOperation(operation: OperationCommand): Promise<OperationResult> {
  return apiJson<OperationResult>('/api/operations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(operation),
  })
}

export function createDownload(url: string, directoryPath: string, filename?: string): Promise<DownloadResponse> {
  return apiJson<DownloadResponse>('/api/downloads', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url, directoryPath, ...(filename ? { filename } : {}) }),
  })
}

export function cancelDownload(id: string): Promise<DownloadResponse> {
  return apiJson<DownloadResponse>(`/api/downloads/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export function retryDownload(id: string): Promise<DownloadResponse> {
  return apiJson<DownloadResponse>(`/api/downloads/${encodeURIComponent(id)}/retry`, { method: 'POST' })
}

export async function clearDownloads(): Promise<void> {
  await apiJson<void>('/api/downloads?terminal=1', { method: 'DELETE' })
}

export function createExtractions(paths: string[]): Promise<ExtractionBatchResponse> {
  return apiJson<ExtractionBatchResponse>('/api/extractions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths }),
  })
}

export function cancelExtraction(id: string): Promise<ExtractionResponse> {
  return apiJson<ExtractionResponse>(`/api/extractions/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export function retryExtraction(id: string): Promise<ExtractionResponse> {
  return apiJson<ExtractionResponse>(`/api/extractions/${encodeURIComponent(id)}/retry`, { method: 'POST' })
}

export async function clearExtractions(): Promise<void> {
  await apiJson<void>('/api/extractions?terminal=1', { method: 'DELETE' })
}

export function uploadFile(
  directoryPath: string,
  file: File,
  onProgress: (progress: number) => void,
  signal?: AbortSignal,
): Promise<UploadResult> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest()
    request.open('POST', `/api/upload?path=${encodeURIComponent(directoryPath)}`)
    request.responseType = 'json'
    const abort = (): void => request.abort()
    const cleanup = (): void => signal?.removeEventListener('abort', abort)
    signal?.addEventListener('abort', abort, { once: true })
    request.upload.onprogress = (event) => {
      if (event.lengthComputable)
        onProgress(Math.round(event.loaded / event.total * 100))
    }
    request.onload = () => {
      if (request.status >= 200 && request.status < 300) {
        cleanup()
        resolve(request.response as UploadResult)
        return
      }
      cleanup()
      if (request.status === 401)
        window.dispatchEvent(new Event('fs-authentication-required'))
      const body = request.response as ErrorBody | null
      reject(new Error(body?.error?.message ?? `Upload failed (${request.status})`))
    }
    request.onerror = () => {
      cleanup()
      reject(new Error('Upload failed: network error'))
    }
    request.onabort = () => {
      cleanup()
      reject(abortError())
    }
    const body = new FormData()
    body.set('file', file)
    request.send(body)
  })
}

function delay(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(abortError())
      return
    }
    let timer: number
    const abort = (): void => {
      window.clearTimeout(timer)
      reject(abortError())
    }
    timer = window.setTimeout(() => {
      signal.removeEventListener('abort', abort)
      resolve()
    }, milliseconds)
    signal.addEventListener('abort', abort, { once: true })
  })
}

function uploadChunk(id: string, chunk: Blob, start: number, total: number, onProgress: (loaded: number) => void, signal: AbortSignal): Promise<ChunkedUpload> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(abortError())
      return
    }
    const request = new XMLHttpRequest()
    const end = start + chunk.size - 1
    const abort = (): void => request.abort()
    const cleanup = (): void => signal.removeEventListener('abort', abort)
    request.open('PUT', `/api/uploads/${encodeURIComponent(id)}`)
    request.responseType = 'json'
    request.setRequestHeader('Content-Type', 'application/octet-stream')
    request.setRequestHeader('Content-Range', `bytes ${start}-${end}/${total}`)
    request.upload.onprogress = event => onProgress(start + event.loaded)
    request.onload = () => {
      cleanup()
      if (request.status >= 200 && request.status < 300) {
        resolve((request.response as ChunkedUploadResponse).upload)
        return
      }
      if (request.status === 401)
        window.dispatchEvent(new Event('fs-authentication-required'))
      const body = request.response as ErrorBody | null
      reject(new ApiError(request.status, body?.error?.message ?? `Upload failed (${request.status})`))
    }
    request.onerror = () => {
      cleanup()
      reject(new Error('Upload failed: network error'))
    }
    request.onabort = () => {
      cleanup()
      reject(abortError())
    }
    signal.addEventListener('abort', abort, { once: true })
    request.send(chunk)
  })
}

async function inspectChunkedUpload(id: string, signal: AbortSignal): Promise<ChunkedUpload> {
  return (await apiJson<ChunkedUploadResponse>(`/api/uploads/${encodeURIComponent(id)}`, { signal })).upload
}

async function completeChunkedUpload(id: string, signal: AbortSignal): Promise<ChunkedUpload> {
  return (await apiJson<ChunkedUploadResponse>(`/api/uploads/${encodeURIComponent(id)}/complete`, { method: 'POST', signal })).upload
}

async function cancelChunkedUpload(id: string): Promise<void> {
  await fetch(`/api/uploads/${encodeURIComponent(id)}`, { method: 'DELETE', keepalive: true }).catch(() => {})
}

async function inspectChunkedUploadWithRetry(id: string, signal: AbortSignal, onDetail?: (detail: string) => void): Promise<ChunkedUpload> {
  for (let attempt = 0; attempt <= 3; attempt++) {
    try {
      return await inspectChunkedUpload(id, signal)
    }
    catch (cause) {
      if (signal.aborted || attempt === 3)
        throw cause
      onDetail?.(`Checking upload status again (${attempt + 1}/3)`)
      await delay(250 * 2 ** attempt, signal)
    }
  }
  throw new Error('Upload status could not be read')
}

export async function uploadChunkedFile(
  directoryPath: string,
  file: File,
  capability: NonNullable<DirectoryListing['chunkedUpload']>,
  onProgress: (progress: number) => void,
  signal: AbortSignal,
  onDetail?: (detail: string) => void,
): Promise<UploadResult> {
  let id: string | undefined
  try {
    const created = await apiJson<ChunkedUploadResponse>('/api/uploads', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ directoryPath, filename: file.name, size: file.size }),
      signal,
    })
    id = created.upload.id
    let upload = created.upload
    let retryCount = 0
    while (upload.status === 'uploading' && upload.uploadedBytes < file.size) {
      if (signal.aborted)
        throw abortError()
      const start = upload.uploadedBytes
      const chunk = file.slice(start, Math.min(start + capability.chunkSizeBytes, file.size))
      const chunkNumber = Math.floor(start / capability.chunkSizeBytes) + 1
      onDetail?.(`Uploading chunk ${chunkNumber}`)
      try {
        upload = await uploadChunk(upload.id, chunk, start, file.size, loaded => onProgress(Math.round(loaded / file.size * 100)), signal)
        onProgress(Math.round(upload.uploadedBytes / file.size * 100))
        retryCount = 0
      }
      catch (cause) {
        if (signal.aborted || (cause instanceof DOMException && cause.name === 'AbortError'))
          throw cause
        if (++retryCount > 3)
          throw cause
        onDetail?.(`Retrying chunk ${chunkNumber} (${retryCount}/3)`)
        await delay(250 * 2 ** (retryCount - 1), signal)
        upload = await inspectChunkedUploadWithRetry(upload.id, signal, onDetail)
        if (upload.status === 'complete')
          return upload.result
      }
    }
    for (let attempt = 0; attempt <= 3; attempt++) {
      if (signal.aborted)
        throw abortError()
      try {
        const completed = await completeChunkedUpload(id, signal)
        if (completed.status === 'complete')
          return completed.result
      }
      catch (cause) {
        if (signal.aborted || attempt === 3)
          throw cause
        onDetail?.(`Retrying completion (${attempt + 1}/3)`)
        await delay(250 * 2 ** attempt, signal)
        const current = await inspectChunkedUploadWithRetry(id, signal, onDetail)
        if (current.status === 'complete')
          return current.result
      }
    }
    throw new Error('Upload could not be completed')
  }
  catch (cause) {
    if (id)
      void cancelChunkedUpload(id)
    throw cause
  }
}
