import type { FsExtractionManager, FsExtractionTask, FsWorkspace } from './types'

const DEFAULT_MAX_QUEUED = 100
const DEFAULT_MAX_TASKS = 100
const MAX_BATCH_PATHS = 100
const MAX_PATH_LENGTH = 4096

export type ExtractionErrorCode
  = | 'INVALID_EXTRACTION'
    | 'EXTRACTION_NOT_FOUND'
    | 'EXTRACTION_ACTIVE'
    | 'EXTRACTION_QUEUE_FULL'
    | 'EXTRACTION_SERVICE_STOPPED'

export class ExtractionError extends Error {
  constructor(readonly code: ExtractionErrorCode, message: string) {
    super(message)
    this.name = 'ExtractionError'
  }
}

export interface ExtractionManagerOptions {
  maxQueued?: number
  maxTasks?: number
}

interface InternalTask extends FsExtractionTask {
  controller?: AbortController
}

function publicTask(task: InternalTask): FsExtractionTask {
  const { controller: _controller, ...result } = task
  return { ...result }
}

function validatePaths(paths: string[]): string[] {
  if (paths.length === 0 || paths.length > MAX_BATCH_PATHS)
    throw new ExtractionError('INVALID_EXTRACTION', `Extraction requests must contain between 1 and ${MAX_BATCH_PATHS} paths`)
  for (const archivePath of paths) {
    if (!archivePath || archivePath.length > MAX_PATH_LENGTH)
      throw new ExtractionError('INVALID_EXTRACTION', 'Archive path is invalid')
  }
  return paths
}

export function createExtractionManager(
  workspace: Pick<FsWorkspace, 'extractArchive'>,
  options: ExtractionManagerOptions = {},
): FsExtractionManager {
  const maxQueued = Math.max(1, options.maxQueued ?? DEFAULT_MAX_QUEUED)
  const maxTasks = Math.max(1, options.maxTasks ?? DEFAULT_MAX_TASKS)
  const tasks = new Map<string, InternalTask>()
  const queue: string[] = []
  const runs = new Set<Promise<void>>()
  const listeners = new Set<(tasks: FsExtractionTask[]) => void>()
  let active: InternalTask | undefined
  let closed = false
  let lastEmit = 0

  const list = (): FsExtractionTask[] => [...tasks.values()].reverse().map(publicTask)
  const emit = (force = false): void => {
    const now = performance.now()
    if (!force && now - lastEmit < 250)
      return
    lastEmit = now
    const snapshot = list()
    for (const listener of listeners)
      listener(snapshot)
  }
  const trim = (requiredSlots = 0): void => {
    for (const [id, task] of tasks) {
      if (tasks.size + requiredSlots <= maxTasks)
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

  const run = async (task: InternalTask, controller: AbortController): Promise<void> => {
    update(task, { status: 'running', startedAt: new Date().toISOString() }, true)
    try {
      const result = await workspace.extractArchive(task.archivePath, {
        signal: controller.signal,
        onInspect(details) {
          update(task, { ...details, progress: task.progress ?? 0 }, true)
        },
        onProgress(progress) {
          update(task, { progress })
        },
      })
      if (task.status !== 'cancelled') {
        update(task, {
          status: 'done',
          destinationPath: result.destinationPath,
          uncompressedBytes: result.uncompressedBytes,
          entryCount: result.entryCount,
          progress: 100,
          finishedAt: new Date().toISOString(),
        }, true)
      }
    }
    catch (cause) {
      if (task.status === 'cancelled' || controller.signal.aborted) {
        update(task, { status: 'cancelled', finishedAt: new Date().toISOString() }, true)
      }
      else {
        update(task, {
          status: 'error',
          error: cause instanceof Error ? cause.message : String(cause),
          finishedAt: new Date().toISOString(),
        }, true)
      }
    }
  }

  const pump = (): void => {
    if (closed || active)
      return
    while (queue.length > 0) {
      const id = queue.shift()!
      const task = tasks.get(id)
      if (!task || task.status !== 'queued')
        continue
      const controller = new AbortController()
      task.controller = controller
      active = task
      const promise = run(task, controller).finally(() => {
        task.controller = undefined
        active = undefined
        runs.delete(promise)
        trim()
        pump()
      })
      runs.add(promise)
      return
    }
  }

  const enqueue = async (paths: string[]): Promise<FsExtractionTask[]> => {
    if (closed)
      throw new ExtractionError('EXTRACTION_SERVICE_STOPPED', 'Extraction service is stopped')
    const archivePaths = validatePaths(paths)
    if (queue.length + archivePaths.length > maxQueued)
      throw new ExtractionError('EXTRACTION_QUEUE_FULL', 'Extraction queue is full')
    trim(archivePaths.length)
    if (tasks.size + archivePaths.length > maxTasks)
      throw new ExtractionError('EXTRACTION_QUEUE_FULL', 'Extraction task history is full while tasks are active')

    const created = archivePaths.map((archivePath): InternalTask => ({
      id: crypto.randomUUID(),
      archivePath,
      status: 'queued',
      createdAt: new Date().toISOString(),
    }))
    for (const task of created) {
      tasks.set(task.id, task)
      queue.push(task.id)
    }
    emit(true)
    pump()
    return created.map(publicTask)
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
        task.controller?.abort(new Error('Extraction cancelled'))
        emit(true)
      }
      return publicTask(task)
    },
    async retry(id) {
      const task = tasks.get(id)
      if (!task)
        throw new ExtractionError('EXTRACTION_NOT_FOUND', 'Extraction task was not found')
      if (!['error', 'cancelled'].includes(task.status))
        throw new ExtractionError('EXTRACTION_ACTIVE', 'Only failed or cancelled extractions can be retried')
      const [retried] = await enqueue([task.archivePath])
      return retried!
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
      active?.controller?.abort(new Error('Server stopped'))
      emit(true)
      await Promise.allSettled([...runs])
      listeners.clear()
    },
  }
}
