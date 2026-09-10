import type { ThumbnailWorkerRequest, ThumbnailWorkerResponse } from './thumbnail-worker'
import type { FsFile, FsWorkspace } from './types'
import { imageDimensionsFromData } from 'image-dimensions'

export const THUMBNAIL_MAX_INPUT_BYTES = 64 * 1024 * 1024
export const THUMBNAIL_MAX_INPUT_PIXELS = 50_000_000

const THUMBNAIL_CACHE_MAX_BYTES = 32 * 1024 * 1024
const THUMBNAIL_CACHE_MAX_ENTRIES = 1000
const THUMBNAIL_MAX_QUEUED = 128
const THUMBNAIL_TIMEOUT_MS = 5000
const THUMBNAIL_WORKER_COUNT = 2
const SUPPORTED_MIME_TYPES = new Set(['image/avif', 'image/gif', 'image/jpeg', 'image/png', 'image/webp'])

interface ThumbnailTask {
  id: number
  bytes: ArrayBuffer
  resolve: (bytes: Uint8Array<ArrayBuffer>) => void
  reject: (cause: Error) => void
  timeout?: ReturnType<typeof setTimeout>
}

interface WorkerSlot {
  worker: Worker
  task?: ThumbnailTask
}

interface ThumbnailWorkerPoolOptions {
  createWorker?: () => Worker
  maxQueued?: number
  timeoutMs?: number
  workerCount?: number
}

export class ThumbnailError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
    this.name = 'ThumbnailError'
  }
}

export interface ThumbnailResult {
  bytes: Uint8Array<ArrayBuffer>
  etag: string
  modifiedAt: Date
}

class ThumbnailWorkerPool {
  private readonly queue: ThumbnailTask[] = []
  private readonly slots: WorkerSlot[] = []
  private nextId = 1
  private closed = false

  constructor(private readonly options: ThumbnailWorkerPoolOptions = {}) {}

  convert(bytes: Uint8Array): Promise<Uint8Array<ArrayBuffer>> {
    if (this.closed)
      return Promise.reject(new ThumbnailError(503, 'Thumbnail service is stopped'))
    if (this.queue.length >= (this.options.maxQueued ?? THUMBNAIL_MAX_QUEUED))
      return Promise.reject(new ThumbnailError(503, 'Thumbnail queue is full'))

    this.ensureWorkers()
    return new Promise((resolve, reject) => {
      const buffer = new ArrayBuffer(bytes.byteLength)
      new Uint8Array(buffer).set(bytes)
      this.queue.push({ id: this.nextId++, bytes: buffer, resolve, reject })
      this.dispatch()
    })
  }

  close(): void {
    if (this.closed)
      return
    this.closed = true
    const error = new ThumbnailError(503, 'Thumbnail service is stopped')
    for (const task of this.queue.splice(0))
      task.reject(error)
    for (const slot of this.slots) {
      if (slot.task) {
        clearTimeout(slot.task.timeout)
        slot.task.reject(error)
      }
      slot.worker.terminate()
    }
    this.slots.length = 0
  }

  private ensureWorkers(): void {
    while (this.slots.length < (this.options.workerCount ?? THUMBNAIL_WORKER_COUNT))
      this.slots.push(this.createSlot())
  }

  private createSlot(): WorkerSlot {
    const slot = {
      worker: this.options.createWorker?.() ?? new Worker('./src/commands/fs/thumbnail-worker.ts'),
    } as WorkerSlot
    slot.worker.onmessage = (event: MessageEvent<ThumbnailWorkerResponse>) => {
      const task = slot.task
      if (!task || event.data.id !== task.id)
        return
      clearTimeout(task.timeout)
      slot.task = undefined
      if (event.data.ok)
        task.resolve(new Uint8Array(event.data.bytes))
      else
        task.reject(new ThumbnailError(422, event.data.message))
      this.dispatch()
    }
    slot.worker.onerror = event => this.removeFailedWorker(slot, new ThumbnailError(500, event.message || 'Thumbnail worker failed'))
    return slot
  }

  private dispatch(): void {
    if (this.closed)
      return
    for (const slot of this.slots) {
      if (slot.task || this.queue.length === 0)
        continue
      const task = this.queue.shift()!
      slot.task = task
      task.timeout = setTimeout(() => {
        this.replaceFailedWorker(slot, new ThumbnailError(504, 'Thumbnail conversion timed out'))
      }, this.options.timeoutMs ?? THUMBNAIL_TIMEOUT_MS)
      const message = { id: task.id, bytes: task.bytes } satisfies ThumbnailWorkerRequest
      slot.worker.postMessage(message, [task.bytes])
    }
  }

  private replaceFailedWorker(slot: WorkerSlot, error: ThumbnailError): void {
    const index = this.slots.indexOf(slot)
    if (index === -1)
      return
    const task = slot.task
    if (task) {
      clearTimeout(task.timeout)
      task.reject(error)
    }
    slot.worker.terminate()
    if (this.closed) {
      this.slots.splice(index, 1)
      return
    }
    this.slots[index] = this.createSlot()
    this.dispatch()
  }

  private removeFailedWorker(slot: WorkerSlot, error: ThumbnailError): void {
    const index = this.slots.indexOf(slot)
    if (index === -1)
      return
    const task = slot.task
    if (task) {
      clearTimeout(task.timeout)
      task.reject(error)
    }
    slot.worker.terminate()
    this.slots.splice(index, 1)
    if (this.slots.length > 0) {
      this.dispatch()
      return
    }
    for (const queued of this.queue.splice(0))
      queued.reject(error)
  }
}

export class ThumbnailService {
  private readonly pool: ThumbnailWorkerPool
  private readonly cache = new Map<string, Uint8Array<ArrayBuffer>>()
  private readonly inFlight = new Map<string, Promise<Uint8Array<ArrayBuffer>>>()
  private cacheBytes = 0

  constructor(private readonly workspace: FsWorkspace, workerOptions: ThumbnailWorkerPoolOptions = {}) {
    this.pool = new ThumbnailWorkerPool(workerOptions)
  }

  async get(relativePath: string): Promise<ThumbnailResult> {
    const file = await this.workspace.openFile(relativePath)
    this.validateFile(file)
    const key = `${relativePath}\0${file.size}\0${file.modifiedAt.getTime()}`
    const etag = `W/"thumb-${file.size}-${file.modifiedAt.getTime()}-160-72"`
    const cached = this.cache.get(key)
    if (cached) {
      this.cache.delete(key)
      this.cache.set(key, cached)
      return { bytes: cached, etag, modifiedAt: file.modifiedAt }
    }

    let pending = this.inFlight.get(key)
    if (!pending) {
      pending = this.generate(file).then((bytes) => {
        this.cache.set(key, bytes)
        this.cacheBytes += bytes.byteLength
        this.evictCache()
        return bytes
      }).finally(() => this.inFlight.delete(key))
      this.inFlight.set(key, pending)
    }
    return { bytes: await pending, etag, modifiedAt: file.modifiedAt }
  }

  close(): void {
    this.pool.close()
    this.cache.clear()
    this.cacheBytes = 0
  }

  private validateFile(file: FsFile): void {
    if (!SUPPORTED_MIME_TYPES.has(file.mimeType.split(';')[0]!.trim().toLowerCase()))
      throw new ThumbnailError(404, 'Thumbnail format is not supported')
    if (file.size > THUMBNAIL_MAX_INPUT_BYTES)
      throw new ThumbnailError(413, 'Image exceeds the thumbnail size limit')
  }

  private async generate(file: FsFile): Promise<Uint8Array<ArrayBuffer>> {
    const bytes = new Uint8Array(await file.body.arrayBuffer())
    const dimensions = imageDimensionsFromData(bytes)
    if (!dimensions)
      throw new ThumbnailError(422, 'Image dimensions could not be read')
    if (dimensions.width * dimensions.height > THUMBNAIL_MAX_INPUT_PIXELS)
      throw new ThumbnailError(413, 'Image exceeds the thumbnail pixel limit')
    return this.pool.convert(bytes)
  }

  private evictCache(): void {
    while (this.cache.size > THUMBNAIL_CACHE_MAX_ENTRIES || this.cacheBytes > THUMBNAIL_CACHE_MAX_BYTES) {
      const oldest = this.cache.entries().next().value as [string, Uint8Array<ArrayBuffer>] | undefined
      if (!oldest)
        break
      this.cache.delete(oldest[0])
      this.cacheBytes -= oldest[1].byteLength
    }
  }
}
