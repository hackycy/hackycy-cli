import { createHash } from 'node:crypto'
import path from 'node:path'
import process from 'node:process'
import { applicationStateRoot } from '../../shared/application-state'

export const DEFAULT_SESSION_IDLE_DAYS = 7
export const DEFAULT_UPLOAD_CHUNK_SIZE_MIB = 8
export const MIN_UPLOAD_CHUNK_SIZE_MIB = 4
export const MAX_UPLOAD_CHUNK_SIZE_MIB = 16

export interface FsSessionOptions {
  sessionDir?: string
  sessionIdleDays?: number
}

export interface FsChunkedUploadOptions {
  chunkedUpload?: boolean
  uploadChunkSizeMiB?: number
}

function positiveDays(value: string | number, label: string): number {
  const days = typeof value === 'number' ? value : Number(value)
  if (!Number.isSafeInteger(days) || days < 1)
    throw new Error(`${label} must be a positive integer number of days`)
  return days
}

function enabled(value: string | boolean | undefined, label: string): boolean {
  if (value === undefined)
    return false
  if (typeof value === 'boolean')
    return value
  if (value === '1' || value.toLowerCase() === 'true')
    return true
  if (value === '0' || value.toLowerCase() === 'false')
    return false
  throw new Error(`${label} must be 0, 1, true, or false`)
}

function chunkSizeMiB(value: string | number, label: string): number {
  const size = typeof value === 'number' ? value : Number(value)
  if (!Number.isSafeInteger(size) || size < MIN_UPLOAD_CHUNK_SIZE_MIB || size > MAX_UPLOAD_CHUNK_SIZE_MIB)
    throw new Error(`${label} must be an integer from ${MIN_UPLOAD_CHUNK_SIZE_MIB} to ${MAX_UPLOAD_CHUNK_SIZE_MIB} MiB`)
  return size
}

export function defaultFsSessionDirectory(directory: string, env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string {
  const workspaceId = createHash('sha256').update(path.resolve(directory)).digest('hex')
  return path.join(applicationStateRoot(env, platform), 'ycy', 'fs', 'sessions', workspaceId)
}

export function resolveFsSessionOptions(input: FsSessionOptions, directory: string, env: NodeJS.ProcessEnv = process.env): { directory: string, idleLifetimeMs: number } {
  const sessionDirectory = input.sessionDir ?? env.YCY_FS_SESSION_DIR ?? defaultFsSessionDirectory(directory, env)
  const rawIdleDays = input.sessionIdleDays ?? env.YCY_FS_SESSION_IDLE_DAYS ?? DEFAULT_SESSION_IDLE_DAYS
  return {
    directory: path.resolve(sessionDirectory),
    idleLifetimeMs: positiveDays(rawIdleDays, 'File session idle lifetime') * 24 * 60 * 60 * 1000,
  }
}

export function resolveFsChunkedUploadOptions(input: FsChunkedUploadOptions, env: NodeJS.ProcessEnv = process.env): { enabled: boolean, chunkSizeBytes: number } {
  const chunkedUpload = enabled(input.chunkedUpload ?? env.YCY_FS_CHUNKED_UPLOAD, 'Chunked upload')
  const rawChunkSize = input.uploadChunkSizeMiB ?? env.YCY_FS_UPLOAD_CHUNK_SIZE_MIB ?? DEFAULT_UPLOAD_CHUNK_SIZE_MIB
  return {
    enabled: chunkedUpload,
    chunkSizeBytes: chunkSizeMiB(rawChunkSize, 'Upload chunk size') * 1024 * 1024,
  }
}
