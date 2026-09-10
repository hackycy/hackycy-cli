import type { FsArchiveExtractOptions, FsArchiveExtractResult, FsExtractionTask } from './types'
import { afterEach, describe, expect, test } from 'bun:test'
import { createExtractionManager, ExtractionError } from './extraction-service'

const managers: Array<{ close: () => Promise<void> }> = []

afterEach(async () => {
  await Promise.all(managers.splice(0).map(manager => manager.close()))
})

async function waitForTask(manager: { list: () => FsExtractionTask[] }, id: string): Promise<FsExtractionTask> {
  for (let index = 0; index < 200; index++) {
    const task = manager.list().find(item => item.id === id)
    if (task && ['done', 'error', 'cancelled'].includes(task.status))
      return task
    await Bun.sleep(2)
  }
  throw new Error('Timed out waiting for extraction task')
}

function result(archivePath: string): FsArchiveExtractResult {
  return {
    archivePath,
    destinationPath: archivePath.replace(/\.[^.]+$/, ''),
    uncompressedBytes: 42,
    entryCount: 3,
  }
}

describe('ExtractionManager', () => {
  test('runs a batch strictly one task at a time and publishes inspection and progress', async () => {
    let running = 0
    let peakRunning = 0
    const releases: Array<() => void> = []
    const manager = createExtractionManager({
      async extractArchive(archivePath, options = {}) {
        running++
        peakRunning = Math.max(peakRunning, running)
        options.onInspect?.({ uncompressedBytes: 42, entryCount: 3 })
        options.onProgress?.(25)
        await new Promise<void>(resolve => releases.push(resolve))
        running--
        return result(archivePath)
      },
    })
    managers.push(manager)

    const created = await manager.enqueue(['one.zip', 'two.tar.gz'])
    await Bun.sleep(5)
    expect(manager.list().find(task => task.id === created[0]!.id)).toEqual(expect.objectContaining({
      status: 'running',
      progress: 25,
      uncompressedBytes: 42,
      entryCount: 3,
    }))
    expect(manager.list().find(task => task.id === created[1]!.id)?.status).toBe('queued')

    releases.shift()?.()
    expect((await waitForTask(manager, created[0]!.id)).status).toBe('done')
    await Bun.sleep(5)
    expect(manager.list().find(task => task.id === created[1]!.id)?.status).toBe('running')
    releases.shift()?.()
    expect(await waitForTask(manager, created[1]!.id)).toEqual(expect.objectContaining({
      status: 'done',
      destinationPath: 'two.tar',
      progress: 100,
    }))
    expect(peakRunning).toBe(1)
  })

  test('cancels queued and running tasks and forwards the abort signal', async () => {
    const started: string[] = []
    const manager = createExtractionManager({
      extractArchive(archivePath, options = {}): Promise<FsArchiveExtractResult> {
        started.push(archivePath)
        return new Promise((_resolve, reject) => {
          options.signal?.addEventListener('abort', () => reject(options.signal?.reason), { once: true })
        })
      },
    })
    managers.push(manager)

    const [first, second] = await manager.enqueue(['one.zip', 'two.zip'])
    await Bun.sleep(5)
    expect(manager.cancel(second!.id)?.status).toBe('cancelled')
    expect(manager.cancel(first!.id)?.status).toBe('cancelled')
    expect((await waitForTask(manager, first!.id)).status).toBe('cancelled')
    expect(started).toEqual(['one.zip'])
  })

  test('keeps failures retryable and creates a new task', async () => {
    let attempts = 0
    const manager = createExtractionManager({
      async extractArchive(archivePath) {
        attempts++
        if (attempts === 1)
          throw new Error('damaged archive')
        return result(archivePath)
      },
    })
    managers.push(manager)

    const [created] = await manager.enqueue(['retry.zip'])
    expect(await waitForTask(manager, created!.id)).toEqual(expect.objectContaining({ status: 'error', error: 'damaged archive' }))
    const retried = await manager.retry(created!.id)
    expect(retried.id).not.toBe(created!.id)
    expect((await waitForTask(manager, retried.id)).status).toBe('done')
  })

  test('rejects invalid batches and preserves the bounded task history', async () => {
    const manager = createExtractionManager({
      async extractArchive(archivePath) {
        return result(archivePath)
      },
    }, { maxTasks: 2 })
    managers.push(manager)

    await expect(manager.enqueue([])).rejects.toBeInstanceOf(ExtractionError)
    await expect(manager.enqueue(Array.from({ length: 101 }, (_, index) => `${index}.zip`))).rejects.toMatchObject({ code: 'INVALID_EXTRACTION' })
    await waitForTask(manager, (await manager.enqueue(['one.zip']))[0]!.id)
    await waitForTask(manager, (await manager.enqueue(['two.zip']))[0]!.id)
    await waitForTask(manager, (await manager.enqueue(['three.zip']))[0]!.id)

    expect(manager.list()).toHaveLength(2)
    expect(manager.list().map(task => task.archivePath)).toEqual(['three.zip', 'two.zip'])
  })

  test('close cancels the active task and all queued tasks', async () => {
    let activeSignal: AbortSignal | undefined
    const manager = createExtractionManager({
      extractArchive(_archivePath: string, options?: FsArchiveExtractOptions): Promise<FsArchiveExtractResult> {
        activeSignal = options?.signal
        return new Promise((_resolve, reject) => {
          options?.signal?.addEventListener('abort', () => reject(options.signal?.reason), { once: true })
        })
      },
    })
    managers.push(manager)

    await manager.enqueue(['one.zip', 'two.zip'])
    await Bun.sleep(5)
    await manager.close()

    expect(activeSignal?.aborted).toBe(true)
    expect(manager.list().every(task => task.status === 'cancelled')).toBe(true)
    await expect(manager.enqueue(['three.zip'])).rejects.toMatchObject({ code: 'EXTRACTION_SERVICE_STOPPED' })
  })
})
