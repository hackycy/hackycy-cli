import type { FsChunkedUpload, FsUploadResult, FsWorkspace } from './types'

export const CHUNKED_UPLOAD_THRESHOLD_BYTES = 20 * 1024 * 1024
export const CHUNKED_UPLOAD_IDLE_MS = 30 * 60 * 1000
export const CHUNKED_UPLOAD_TERMINAL_MS = 5 * 60 * 1000
export const CHUNKED_UPLOAD_MAX_SESSIONS = 100
export const CHUNKED_UPLOAD_MAX_SESSIONS_PER_OWNER = 3
export const CHUNKED_UPLOAD_WRITE_TIMEOUT_MS = 5 * 60 * 1000

export type ChunkedUploadErrorCode
  = | 'INVALID_CHUNKED_UPLOAD'
    | 'CHUNKED_UPLOAD_NOT_FOUND'
    | 'CHUNKED_UPLOAD_OFFSET_MISMATCH'
    | 'CHUNKED_UPLOAD_ACTIVE'
    | 'CHUNKED_UPLOAD_INCOMPLETE'
    | 'CHUNKED_UPLOAD_LIMIT_REACHED'
    | 'CHUNKED_UPLOAD_TIMEOUT'
    | 'CHUNKED_UPLOAD_STOPPED'

export class ChunkedUploadError extends Error {
  constructor(readonly code: ChunkedUploadErrorCode, message: string) {
    super(message)
    this.name = 'ChunkedUploadError'
  }
}

export interface ChunkedUploadRequest {
  directoryPath: string
  filename: string
  size: number
}

export interface ChunkedUploadPart {
  start: number
  end: number
  total: number
}

export type ChunkedUploadStatus
  = | { id: string, status: 'uploading', size: number, uploadedBytes: number, chunkSizeBytes: number }
    | { id: string, status: 'complete', size: number, uploadedBytes: number, chunkSizeBytes: number, result: FsUploadResult }

export interface FsChunkedUploadManager {
  create: (request: ChunkedUploadRequest, owner: string) => Promise<ChunkedUploadStatus>
  get: (id: string, owner: string) => Promise<ChunkedUploadStatus>
  write: (id: string, owner: string, part: ChunkedUploadPart, stream: ReadableStream<Uint8Array>, signal?: AbortSignal) => Promise<ChunkedUploadStatus>
  complete: (id: string, owner: string) => Promise<ChunkedUploadStatus>
  cancel: (id: string, owner: string) => Promise<ChunkedUploadStatus>
  close: () => Promise<void>
}

interface UploadSession {
  id: string
  owner: string
  size: number
  uploadedBytes: number
  upload: FsChunkedUpload
  status: 'uploading' | 'complete'
  result?: FsUploadResult
  lastTouchedAt: number
  terminalAt?: number
  writing: boolean
  completing?: Promise<ChunkedUploadStatus>
}

function uploadStatus(session: UploadSession, chunkSizeBytes: number): ChunkedUploadStatus {
  const base = {
    id: session.id,
    size: session.size,
    uploadedBytes: session.uploadedBytes,
    chunkSizeBytes,
  }
  if (session.status === 'complete')
    return { ...base, status: 'complete', result: session.result! }
  return { ...base, status: 'uploading' }
}

export function createChunkedUploadManager(workspace: FsWorkspace, options: {
  chunkSizeBytes: number
  idleMs?: number
  terminalMs?: number
  maxSessions?: number
  maxSessionsPerOwner?: number
  writeTimeoutMs?: number
  now?: () => number
}): FsChunkedUploadManager {
  const chunkSizeBytes = options.chunkSizeBytes
  const idleMs = options.idleMs ?? CHUNKED_UPLOAD_IDLE_MS
  const terminalMs = options.terminalMs ?? CHUNKED_UPLOAD_TERMINAL_MS
  const maxSessions = options.maxSessions ?? CHUNKED_UPLOAD_MAX_SESSIONS
  const maxSessionsPerOwner = options.maxSessionsPerOwner ?? CHUNKED_UPLOAD_MAX_SESSIONS_PER_OWNER
  const writeTimeoutMs = options.writeTimeoutMs ?? CHUNKED_UPLOAD_WRITE_TIMEOUT_MS
  const now = options.now ?? (() => Date.now())
  const sessions = new Map<string, UploadSession>()
  const creatingByOwner = new Map<string, number>()
  let creating = 0
  let closed = false
  let sweeping: Promise<void> | undefined
  const runs = new Set<Promise<unknown>>()

  if (!Number.isSafeInteger(writeTimeoutMs) || writeTimeoutMs <= 0)
    throw new Error('Chunked upload write timeout must be a positive integer')

  const activeCountForOwner = (owner: string): number => [...sessions.values()].filter(session => session.owner === owner && session.status === 'uploading').length

  const track = <T>(run: Promise<T>): Promise<T> => {
    runs.add(run)
    run.then(
      () => runs.delete(run),
      () => runs.delete(run),
    )
    return run
  }

  const sweep = async (): Promise<void> => {
    if (sweeping)
      return sweeping
    sweeping = (async () => {
      const current = now()
      await Promise.all([...sessions.values()].map(async (session) => {
        if (session.status === 'complete') {
          if (session.terminalAt !== undefined && current - session.terminalAt >= terminalMs)
            sessions.delete(session.id)
          return
        }
        if (!session.writing && !session.completing && current - session.lastTouchedAt >= idleMs) {
          sessions.delete(session.id)
          await session.upload.abort()
        }
      }))
    })().finally(() => {
      sweeping = undefined
    })
    return sweeping
  }
  const timer = setInterval(() => {
    void sweep()
  }, Math.min(idleMs, 60_000))
  timer.unref?.()

  const requireSession = async (id: string, owner: string): Promise<UploadSession> => {
    await sweep()
    const session = sessions.get(id)
    if (!session || session.owner !== owner)
      throw new ChunkedUploadError('CHUNKED_UPLOAD_NOT_FOUND', 'Upload session was not found')
    return session
  }

  return {
    async create(request, owner) {
      return await track((async () => {
        await sweep()
        if (closed)
          throw new ChunkedUploadError('CHUNKED_UPLOAD_STOPPED', 'Upload service has stopped')
        if (!Number.isSafeInteger(request.size) || request.size <= CHUNKED_UPLOAD_THRESHOLD_BYTES)
          throw new ChunkedUploadError('INVALID_CHUNKED_UPLOAD', 'Chunked uploads must exceed 20 MiB')
        if (sessions.size + creating >= maxSessions)
          throw new ChunkedUploadError('CHUNKED_UPLOAD_LIMIT_REACHED', 'Too many upload sessions are retained')
        if (activeCountForOwner(owner) + (creatingByOwner.get(owner) ?? 0) >= maxSessionsPerOwner)
          throw new ChunkedUploadError('CHUNKED_UPLOAD_LIMIT_REACHED', 'Too many uploads are active for this session')

        creating++
        creatingByOwner.set(owner, (creatingByOwner.get(owner) ?? 0) + 1)
        try {
          const upload = await workspace.beginChunkedUpload(request.directoryPath, request.filename, request.size)
          if (closed) {
            await upload.abort()
            throw new ChunkedUploadError('CHUNKED_UPLOAD_STOPPED', 'Upload service has stopped')
          }
          const session: UploadSession = {
            id: crypto.randomUUID(),
            owner,
            size: request.size,
            uploadedBytes: 0,
            upload,
            status: 'uploading',
            lastTouchedAt: now(),
            writing: false,
          }
          sessions.set(session.id, session)
          return uploadStatus(session, chunkSizeBytes)
        }
        finally {
          creating--
          const remaining = (creatingByOwner.get(owner) ?? 1) - 1
          if (remaining === 0)
            creatingByOwner.delete(owner)
          else
            creatingByOwner.set(owner, remaining)
        }
      })())
    },
    async get(id, owner) {
      const session = await requireSession(id, owner)
      if (session.status === 'uploading')
        session.lastTouchedAt = now()
      return uploadStatus(session, chunkSizeBytes)
    },
    async write(id, owner, part, stream, signal) {
      const session = await requireSession(id, owner)
      if (closed)
        throw new ChunkedUploadError('CHUNKED_UPLOAD_STOPPED', 'Upload service has stopped')
      if (session.status !== 'uploading' || session.writing || session.completing)
        throw new ChunkedUploadError('CHUNKED_UPLOAD_ACTIVE', 'Upload session is busy')
      const expectedLength = part.end - part.start + 1
      if (!Number.isSafeInteger(part.start) || !Number.isSafeInteger(part.end) || !Number.isSafeInteger(part.total))
        throw new ChunkedUploadError('INVALID_CHUNKED_UPLOAD', 'Upload range is invalid')
      if (part.total !== session.size || part.start !== session.uploadedBytes || expectedLength <= 0 || expectedLength > chunkSizeBytes || part.end >= session.size)
        throw new ChunkedUploadError(part.start !== session.uploadedBytes ? 'CHUNKED_UPLOAD_OFFSET_MISMATCH' : 'INVALID_CHUNKED_UPLOAD', part.start !== session.uploadedBytes ? 'Upload offset does not match the confirmed bytes' : 'Upload range is invalid')

      session.writing = true
      const controller = new AbortController()
      let timedOut = false
      const abort = (): void => controller.abort(signal?.reason)
      signal?.addEventListener('abort', abort, { once: true })
      const timer = setTimeout(() => {
        timedOut = true
        controller.abort()
      }, writeTimeoutMs)
      timer.unref?.()
      const run = (async (): Promise<ChunkedUploadStatus> => {
        try {
          await session.upload.writeChunk(part.start, stream, expectedLength, { signal: controller.signal })
          if (timedOut)
            throw new ChunkedUploadError('CHUNKED_UPLOAD_TIMEOUT', 'Upload chunk timed out')
          session.uploadedBytes += expectedLength
          session.lastTouchedAt = now()
          return uploadStatus(session, chunkSizeBytes)
        }
        catch (cause) {
          if (timedOut)
            throw new ChunkedUploadError('CHUNKED_UPLOAD_TIMEOUT', 'Upload chunk timed out')
          throw cause
        }
        finally {
          clearTimeout(timer)
          signal?.removeEventListener('abort', abort)
          session.writing = false
        }
      })()
      return await track(run)
    },
    async complete(id, owner) {
      const session = await requireSession(id, owner)
      if (closed)
        throw new ChunkedUploadError('CHUNKED_UPLOAD_STOPPED', 'Upload service has stopped')
      if (session.status === 'complete')
        return uploadStatus(session, chunkSizeBytes)
      if (session.completing)
        return await session.completing
      if (session.writing)
        throw new ChunkedUploadError('CHUNKED_UPLOAD_ACTIVE', 'Upload session is busy')
      if (session.uploadedBytes !== session.size)
        throw new ChunkedUploadError('CHUNKED_UPLOAD_INCOMPLETE', 'Upload has not received every byte')
      session.lastTouchedAt = now()
      const run = (async (): Promise<ChunkedUploadStatus> => {
        try {
          const result = await session.upload.publish()
          session.status = 'complete'
          session.result = result
          session.terminalAt = now()
          session.lastTouchedAt = session.terminalAt
          return uploadStatus(session, chunkSizeBytes)
        }
        finally {
          session.completing = undefined
        }
      })()
      session.completing = run
      return await track(run)
    },
    async cancel(id, owner) {
      const session = await requireSession(id, owner)
      if (session.status === 'complete')
        return uploadStatus(session, chunkSizeBytes)
      if (session.writing || session.completing)
        throw new ChunkedUploadError('CHUNKED_UPLOAD_ACTIVE', 'Upload session is busy')
      sessions.delete(id)
      await session.upload.abort()
      return uploadStatus(session, chunkSizeBytes)
    },
    async close() {
      if (closed)
        return
      closed = true
      clearInterval(timer)
      await Promise.allSettled([...runs])
      await Promise.all([...sessions.values()].filter(session => session.status === 'uploading').map(session => session.upload.abort()))
      sessions.clear()
    },
  }
}
