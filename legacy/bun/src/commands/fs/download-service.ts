import type {
  FsDownloadManager,
  FsDownloadRequest,
  FsDownloadTask,
  FsWorkspace,
} from './types'
import { isIP } from 'node:net'

const DEFAULT_MAX_CONCURRENT = 2
const DEFAULT_MAX_QUEUED = 100
const DEFAULT_MAX_TASKS = 100
const DEFAULT_IDLE_TIMEOUT_MS = 60_000
const MAX_REDIRECTS = 5
const MAX_URL_LENGTH = 8192
const MAX_PATH_LENGTH = 4096

export type DownloadErrorCode
  = | 'INVALID_DOWNLOAD'
    | 'URL_FORBIDDEN'
    | 'DOWNLOAD_NOT_FOUND'
    | 'DOWNLOAD_ACTIVE'
    | 'DOWNLOAD_QUEUE_FULL'
    | 'DOWNLOAD_UNAVAILABLE'
    | 'DOWNLOAD_SERVICE_STOPPED'

export class DownloadError extends Error {
  constructor(readonly code: DownloadErrorCode, message: string) {
    super(message)
    this.name = 'DownloadError'
  }
}

export interface RemoteDownloadManagerOptions {
  maxConcurrent?: number
  maxQueued?: number
  maxTasks?: number
  idleTimeoutMs?: number
  fetchImpl?: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
}

interface InternalTask extends FsDownloadTask {
  controller?: AbortController
}

function blockedIpv4(address: string): boolean {
  const parts = address.split('.').map(Number)
  if (parts.length !== 4 || parts.some(part => !Number.isInteger(part) || part < 0 || part > 255))
    return true
  const [first, second] = parts as [number, number, number, number]
  return first === 0
    || first === 10
    || (first === 100 && second! >= 64 && second! <= 127)
    || first === 127
    || (first === 169 && second === 254)
    || (first === 172 && second! >= 16 && second! <= 31)
    || (first === 192 && [0, 2, 88, 168].includes(second!))
    || (first === 198 && [18, 19, 51].includes(second!))
    || (first === 203 && second === 0)
    || first >= 224
}

function ipv6Groups(address: string): number[] | undefined {
  const value = address.toLowerCase()
  const pieces = value.split('::')
  if (pieces.length > 2)
    return undefined
  const parse = (part: string): number[] => part
    ? part.split(':').flatMap((item) => {
        if (item.includes('.')) {
          const values = item.split('.').map(Number)
          if (values.length !== 4 || values.some(value => !Number.isInteger(value) || value < 0 || value > 255))
            return []
          return [(values[0]! << 8) | values[1]!, (values[2]! << 8) | values[3]!]
        }
        const parsed = Number.parseInt(item, 16)
        return Number.isInteger(parsed) && parsed >= 0 && parsed <= 0xFFFF ? [parsed] : []
      })
    : []
  const left = parse(pieces[0]!)
  const right = pieces.length === 2 ? parse(pieces[1]!) : []
  if (left.length + right.length > 8 || (pieces.length === 1 && left.length !== 8))
    return undefined
  return pieces.length === 2 ? [...left, ...Array.from({ length: 8 - left.length - right.length }, () => 0), ...right] : left
}

function blockedIpv6(address: string): boolean {
  const groups = ipv6Groups(address)
  if (!groups)
    return true
  const first = groups[0]!
  const mapped = groups.slice(0, 5).every(group => group === 0) && groups[5] === 0xFFFF
  if (mapped)
    return blockedIpv4(`${groups[6]! >> 8}.${groups[6]! & 0xFF}.${groups[7]! >> 8}.${groups[7]! & 0xFF}`)
  return first === 0
    || first === 0xFFFF
    || first >= 0xFF00
    || (first === 0x2001 && groups[1] === 0x0DB8)
    || (first & 0xFE00) === 0xFC00
    || (first & 0xFFC0) === 0xFE80
}

function blockedAddress(address: string): boolean {
  const version = isIP(address)
  return version === 4 ? blockedIpv4(address) : version === 6 ? blockedIpv6(address) : true
}

function hostName(value: string): string {
  return value.startsWith('[') && value.endsWith(']') ? value.slice(1, -1) : value
}

function validateFilename(value: string): string {
  const filename = value.trim()
  if (!filename || filename === '.' || filename === '..' || filename.includes('/') || filename.includes('\\') || filename.includes('\0'))
    throw new DownloadError('INVALID_DOWNLOAD', 'Filename must be a single safe file name')
  return filename
}

function normalizeInputUrl(value: string): URL {
  if (!value.trim() || value.length > MAX_URL_LENGTH)
    throw new DownloadError('INVALID_DOWNLOAD', 'Download URL is invalid')
  let url: URL
  try {
    url = new URL(value)
  }
  catch {
    throw new DownloadError('INVALID_DOWNLOAD', 'Download URL is invalid')
  }
  if (!['http:', 'https:'].includes(url.protocol))
    throw new DownloadError('INVALID_DOWNLOAD', 'Download URL must use HTTP or HTTPS')
  if (url.username || url.password)
    throw new DownloadError('INVALID_DOWNLOAD', 'Download URL cannot contain credentials')
  return url
}

function validateRemoteUrl(input: string | URL): URL {
  const url = normalizeInputUrl(typeof input === 'string' ? input : input.href)
  const hostname = hostName(url.hostname).toLowerCase()
  if (hostname === 'localhost' || hostname.endsWith('.localhost') || hostname.endsWith('.local'))
    throw new DownloadError('URL_FORBIDDEN', 'Download targets cannot use local hostnames')
  if (isIP(hostname) && blockedAddress(hostname))
    throw new DownloadError('URL_FORBIDDEN', 'Download targets cannot use private or reserved addresses')
  return url
}

function safeHeaderFilename(value: string | null | undefined): string | undefined {
  if (!value)
    return undefined
  const encoded = /(?:^|;)\s*filename\*=\s*UTF-8''([^;]+)/i.exec(value)?.[1]
  const plain = /(?:^|;)\s*filename\s*=\s*(?:"([^"]*)"|([^;]+))/i.exec(value)
  const candidate = encoded
    ? (() => {
        try {
          return decodeURIComponent(encoded)
        }
        catch {
          return undefined
        }
      })()
    : plain?.[1] ?? plain?.[2]?.trim()
  if (!candidate)
    return undefined
  const name = candidate.split(/[\\/]/).at(-1)?.trim()
  if (!name)
    return undefined
  try {
    return validateFilename(name)
  }
  catch {
    return undefined
  }
}

function fallbackFilename(url: URL): string {
  let candidate = ''
  try {
    candidate = decodeURIComponent(url.pathname.split('/').filter(Boolean).at(-1) ?? '')
  }
  catch {}
  try {
    return validateFilename(candidate || 'download')
  }
  catch {
    return 'download'
  }
}

function responseFilename(response: Response, url: URL): string {
  return safeHeaderFilename(response.headers.get('Content-Disposition')) ?? fallbackFilename(url)
}

function responseSize(response: Response): number | undefined {
  if (response.headers.get('Content-Encoding'))
    return undefined
  const value = response.headers.get('Content-Length')
  if (!value)
    return undefined
  const size = Number(value)
  if (!Number.isSafeInteger(size) || size < 0)
    throw new DownloadError('DOWNLOAD_UNAVAILABLE', 'Remote response size is too large or invalid')
  return size
}

async function fetchRemote(
  fetchImpl: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
  url: URL,
  signal: AbortSignal,
  timeoutMs: number,
): Promise<Response> {
  const timeout = new AbortController()
  const timer = setTimeout(() => timeout.abort(new DownloadError('DOWNLOAD_UNAVAILABLE', 'Download connection timed out')), timeoutMs)
  try {
    return await fetchImpl(url, {
      redirect: 'manual',
      signal: AbortSignal.any([signal, timeout.signal]),
    })
  }
  finally {
    clearTimeout(timer)
  }
}

function idleTimeoutStream(stream: ReadableStream<Uint8Array>, signal: AbortSignal, timeoutMs: number): ReadableStream<Uint8Array> {
  const reader = stream.getReader()
  let timer: ReturnType<typeof setTimeout> | undefined
  let pendingReject: ((reason?: unknown) => void) | undefined
  let closed = false
  const clearTimer = (): void => {
    if (timer !== undefined)
      clearTimeout(timer)
    timer = undefined
  }
  const abort = (): void => {
    const reason = signal.reason ?? new Error('Download cancelled')
    pendingReject?.(reason)
    void reader.cancel(reason).catch(() => {})
  }
  signal.addEventListener('abort', abort, { once: true })
  return new ReadableStream<Uint8Array>({
    async pull(controller) {
      if (closed)
        return
      try {
        const timeout = new Promise<never>((_resolve, reject) => {
          pendingReject = reject
          timer = setTimeout(() => reject(new Error('Download stalled')), timeoutMs)
        })
        const result = await Promise.race([reader.read(), timeout])
        pendingReject = undefined
        clearTimer()
        if (result.done) {
          closed = true
          signal.removeEventListener('abort', abort)
          controller.close()
          return
        }
        controller.enqueue(result.value)
      }
      catch (error) {
        pendingReject = undefined
        clearTimer()
        closed = true
        signal.removeEventListener('abort', abort)
        await reader.cancel(error).catch(() => {})
        controller.error(error)
      }
    },
    async cancel(reason) {
      closed = true
      pendingReject = undefined
      clearTimer()
      signal.removeEventListener('abort', abort)
      await reader.cancel(reason).catch(() => {})
    },
  })
}

function publicTask(task: InternalTask): FsDownloadTask {
  const { controller: _controller, ...result } = task
  return { ...result }
}

export function createRemoteDownloadManager(
  workspace: FsWorkspace,
  options: RemoteDownloadManagerOptions = {},
): FsDownloadManager {
  const maxConcurrent = Math.max(1, options.maxConcurrent ?? DEFAULT_MAX_CONCURRENT)
  const maxQueued = Math.max(1, options.maxQueued ?? DEFAULT_MAX_QUEUED)
  const maxTasks = Math.max(maxQueued, options.maxTasks ?? DEFAULT_MAX_TASKS)
  const idleTimeoutMs = Math.max(1, options.idleTimeoutMs ?? DEFAULT_IDLE_TIMEOUT_MS)
  const fetchImpl = options.fetchImpl ?? fetch
  const tasks = new Map<string, InternalTask>()
  const queue: string[] = []
  const active = new Map<string, AbortController>()
  const runs = new Set<Promise<void>>()
  const listeners = new Set<(tasks: FsDownloadTask[]) => void>()
  let closed = false
  let lastEmit = 0

  const list = (): FsDownloadTask[] => [...tasks.values()].reverse().map(publicTask)
  const emit = (force = false): void => {
    const now = performance.now()
    if (!force && now - lastEmit < 250)
      return
    lastEmit = now
    const snapshot = list()
    for (const listener of listeners)
      listener(snapshot)
  }
  const trim = (): void => {
    if (tasks.size <= maxTasks)
      return
    for (const [id, task] of tasks) {
      if (tasks.size <= maxTasks)
        break
      if (['done', 'error', 'cancelled'].includes(task.status))
        tasks.delete(id)
    }
  }
  const update = (task: InternalTask, patch: Partial<InternalTask>, force = false): void => {
    Object.assign(task, patch)
    trim()
    emit(force)
  }

  const download = async (task: InternalTask, controller: AbortController): Promise<void> => {
    let currentUrl = validateRemoteUrl(task.url)
    for (let redirect = 0; redirect <= MAX_REDIRECTS; redirect++) {
      const response = await fetchRemote(fetchImpl, currentUrl, controller.signal, idleTimeoutMs)
      if ([301, 302, 303, 307, 308].includes(response.status)) {
        if (redirect === MAX_REDIRECTS)
          throw new DownloadError('DOWNLOAD_UNAVAILABLE', 'Too many redirects')
        const location = response.headers.get('Location')
        await response.body?.cancel()
        if (!location)
          throw new DownloadError('DOWNLOAD_UNAVAILABLE', 'Download redirect did not include a location')
        currentUrl = validateRemoteUrl(new URL(location, currentUrl))
        continue
      }
      if (response.status < 200 || response.status >= 300) {
        await response.body?.cancel()
        throw new DownloadError('DOWNLOAD_UNAVAILABLE', `Remote server returned HTTP ${response.status}`)
      }
      if (!response.body)
        throw new DownloadError('DOWNLOAD_UNAVAILABLE', 'Remote response has no body')

      task.filename ??= responseFilename(response, currentUrl)
      task.totalBytes = responseSize(response)
      const started = performance.now()
      const body = idleTimeoutStream(response.body as ReadableStream<Uint8Array>, controller.signal, idleTimeoutMs)
      const result = await workspace.writeFileStream(task.directoryPath, task.filename, body, {
        signal: controller.signal,
        onProgress(bytesDownloaded) {
          const elapsed = Math.max((performance.now() - started) / 1000, 0.001)
          const progress = task.totalBytes === undefined ? undefined : Math.min(100, Math.round(bytesDownloaded / task.totalBytes * 100))
          update(task, {
            bytesDownloaded,
            progress,
            speedBytesPerSecond: Math.round(bytesDownloaded / elapsed),
          })
        },
      })
      update(task, {
        bytesDownloaded: result.size,
        progress: task.totalBytes === undefined ? undefined : 100,
        destinationPath: result.path,
        filename: result.filename,
        speedBytesPerSecond: task.speedBytesPerSecond ?? 0,
      }, true)
      return
    }
    throw new DownloadError('DOWNLOAD_UNAVAILABLE', 'Download redirect failed')
  }

  const run = async (task: InternalTask, controller: AbortController): Promise<void> => {
    update(task, { status: 'running', startedAt: new Date().toISOString() }, true)
    try {
      await download(task, controller)
      if (task.status !== 'cancelled')
        update(task, { status: 'done', finishedAt: new Date().toISOString() }, true)
    }
    catch (cause) {
      if (task.status === 'cancelled' || controller.signal.aborted) {
        update(task, { status: 'cancelled', finishedAt: new Date().toISOString() }, true)
      }
      else {
        const message = cause instanceof Error ? cause.message : String(cause)
        update(task, { status: 'error', error: message, finishedAt: new Date().toISOString() }, true)
      }
    }
  }

  const pump = (): void => {
    if (closed)
      return
    while (active.size < maxConcurrent && queue.length > 0) {
      const id = queue.shift()!
      const task = tasks.get(id)
      if (!task || task.status !== 'queued')
        continue
      const controller = new AbortController()
      task.controller = controller
      active.set(id, controller)
      const promise = run(task, controller).finally(() => {
        active.delete(id)
        task.controller = undefined
        runs.delete(promise)
        trim()
        pump()
      })
      runs.add(promise)
    }
  }

  const enqueue = async (request: FsDownloadRequest): Promise<FsDownloadTask> => {
    if (closed)
      throw new DownloadError('DOWNLOAD_SERVICE_STOPPED', 'Download service is stopped')
    if (queue.length >= maxQueued)
      throw new DownloadError('DOWNLOAD_QUEUE_FULL', 'Download queue is full')
    const url = validateRemoteUrl(request.url)
    if (typeof request.directoryPath !== 'string' || request.directoryPath.length > MAX_PATH_LENGTH)
      throw new DownloadError('INVALID_DOWNLOAD', 'Download directory is invalid')
    const filename = request.filename === undefined ? undefined : validateFilename(request.filename)
    if (queue.length >= maxQueued)
      throw new DownloadError('DOWNLOAD_QUEUE_FULL', 'Download queue is full')
    const task: InternalTask = {
      id: crypto.randomUUID(),
      url: url.href,
      directoryPath: request.directoryPath,
      ...(filename ? { filename } : {}),
      status: 'queued',
      bytesDownloaded: 0,
      createdAt: new Date().toISOString(),
    }
    tasks.set(task.id, task)
    queue.push(task.id)
    trim()
    emit(true)
    pump()
    return publicTask(task)
  }

  return {
    list,
    enqueue,
    cancel(id) {
      const task = tasks.get(id)
      if (!task)
        return undefined
      if (task.status === 'queued') {
        task.status = 'cancelled'
        task.finishedAt = new Date().toISOString()
        const index = queue.indexOf(id)
        if (index !== -1)
          queue.splice(index, 1)
        emit(true)
      }
      else if (task.status === 'running') {
        task.status = 'cancelled'
        task.finishedAt = new Date().toISOString()
        task.controller?.abort(new Error('Download cancelled'))
        emit(true)
      }
      return publicTask(task)
    },
    async retry(id) {
      const task = tasks.get(id)
      if (!task)
        throw new DownloadError('DOWNLOAD_NOT_FOUND', 'Download task was not found')
      if (!['error', 'cancelled'].includes(task.status))
        throw new DownloadError('DOWNLOAD_ACTIVE', 'Only failed or cancelled downloads can be retried')
      return enqueue({ url: task.url, directoryPath: task.directoryPath, filename: task.filename })
    },
    clearTerminal() {
      for (const [id, task] of tasks) {
        if (['done', 'error', 'cancelled'].includes(task.status))
          tasks.delete(id)
      }
      emit(true)
    },
    subscribe(listener) {
      listeners.add(listener)
      listener(list())
      return () => listeners.delete(listener)
    },
    async close() {
      if (closed)
        return
      closed = true
      for (const id of queue.splice(0)) {
        const task = tasks.get(id)
        if (task) {
          task.status = 'cancelled'
          task.finishedAt = new Date().toISOString()
        }
      }
      for (const controller of active.values())
        controller.abort(new Error('Server stopped'))
      emit(true)
      await Promise.allSettled([...runs])
      listeners.clear()
    },
  }
}
