import type { FsDownloadTask, FsWorkspace } from './types'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { createRemoteDownloadManager } from './download-service'
import { createFsWorkspace } from './workspace'

const temporaryDirectories: string[] = []
const managers: Array<{ close: () => Promise<void> }> = []

afterEach(async () => {
  await Promise.all(managers.splice(0).map(manager => manager.close()))
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function createFixture(): Promise<{ workspace: FsWorkspace, root: string }> {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-fs-download-'))
  temporaryDirectories.push(root)
  const workspace = await createFsWorkspace(root)
  return { workspace, root }
}

async function waitForTask(manager: { list: () => FsDownloadTask[] }, id: string): Promise<FsDownloadTask> {
  for (let index = 0; index < 100; index++) {
    const task = manager.list().find(item => item.id === id)
    if (task && ['done', 'error', 'cancelled'].includes(task.status))
      return task
    await Bun.sleep(2)
  }
  throw new Error('Timed out waiting for download task')
}

describe('RemoteDownloadManager', () => {
  test('streams a response into the workspace and publishes progress', async () => {
    const fetchImpl = async (): Promise<Response> => new Response(Uint8Array.from([1, 2, 3, 4, 5]), {
      headers: {
        'Content-Disposition': 'attachment; filename*=UTF-8\'\'remote%20file.bin',
        'Content-Length': '5',
      },
    })
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace, {
      fetchImpl,
      idleTimeoutMs: 100,
    })
    managers.push(manager)
    const updates: FsDownloadTask[][] = []
    manager.subscribe(tasks => updates.push(tasks))

    const created = await manager.enqueue({ url: 'https://unresolvable.invalid/file', directoryPath: '' })
    const finished = await waitForTask(manager, created.id)

    expect(finished).toEqual(expect.objectContaining({
      status: 'done',
      filename: 'remote file.bin',
      destinationPath: 'remote file.bin',
      bytesDownloaded: 5,
      totalBytes: 5,
      progress: 100,
    }))
    expect(Array.from(new Uint8Array(await (await workspace.openFile('remote file.bin')).body.arrayBuffer()))).toEqual([1, 2, 3, 4, 5])
    expect(updates.some(tasks => tasks.some(task => task.status === 'running'))).toBe(true)
  })

  test('rejects unsafe URLs before creating tasks', async () => {
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace)
    managers.push(manager)

    await expect(manager.enqueue({ url: 'ftp://example.test/file', directoryPath: '' })).rejects.toThrow('HTTP or HTTPS')
    await expect(manager.enqueue({ url: 'http://127.0.0.1/file', directoryPath: '' })).rejects.toThrow('private or reserved')
    await expect(manager.enqueue({ url: 'https://user:pass@example.test/file', directoryPath: '' })).rejects.toThrow('credentials')
    expect(manager.list()).toHaveLength(0)
  })

  test('keeps failures retryable and creates a new task', async () => {
    let attempts = 0
    const fetchImpl = async (): Promise<Response> => {
      attempts++
      return attempts === 1 ? new Response('nope', { status: 503 }) : new Response('ok', { headers: { 'Content-Length': '2' } })
    }
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace, {
      fetchImpl,
    })
    managers.push(manager)

    const failed = await manager.enqueue({ url: 'https://example.test/retry', directoryPath: '', filename: 'retry.txt' })
    expect((await waitForTask(manager, failed.id)).status).toBe('error')
    const retried = await manager.retry(failed.id)
    expect(retried.id).not.toBe(failed.id)
    expect((await waitForTask(manager, retried.id)).status).toBe('done')
    expect(await (await workspace.openFile('retry.txt')).body.text()).toBe('ok')
  })

  test('revalidates redirects before fetching their target', async () => {
    let fetchCalls = 0
    const fetchImpl = async (): Promise<Response> => {
      fetchCalls++
      return new Response(null, { status: 302, headers: { Location: 'http://127.0.0.1/private' } })
    }
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace, {
      fetchImpl,
    })
    managers.push(manager)

    const created = await manager.enqueue({ url: 'https://example.test/redirect', directoryPath: '' })
    const failed = await waitForTask(manager, created.id)

    expect(failed.status).toBe('error')
    expect(failed.error).toContain('private or reserved')
    expect(fetchCalls).toBe(1)
  })

  test('aborts active fetches when a running task is cancelled', async () => {
    const fetchImpl = (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    })
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace, {
      fetchImpl,
    })
    managers.push(manager)

    const created = await manager.enqueue({ url: 'https://example.test/slow', directoryPath: '', filename: 'slow.bin' })
    await Bun.sleep(5)
    expect(manager.cancel(created.id)?.status).toBe('cancelled')
    expect((await waitForTask(manager, created.id)).status).toBe('cancelled')
    expect((await workspace.listDirectory('')).entries).toHaveLength(0)
  })

  test('fails connections that do not produce response headers in time', async () => {
    const fetchImpl = (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    })
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace, {
      fetchImpl,
      idleTimeoutMs: 10,
    })
    managers.push(manager)

    const created = await manager.enqueue({ url: 'https://example.test/stalled', directoryPath: '' })
    const failed = await waitForTask(manager, created.id)

    expect(failed.status).toBe('error')
    expect(failed.error).toBe('Download connection timed out')
  })

  test('cancels queued tasks without starting their fetch', async () => {
    let fetchCalls = 0
    let releaseFirst: (() => void) | undefined
    const fetchImpl = async (): Promise<Response> => {
      fetchCalls++
      if (fetchCalls === 1) {
        await new Promise<void>(resolve => releaseFirst = resolve)
      }
      return new Response('ok')
    }
    const { workspace } = await createFixture()
    const manager = createRemoteDownloadManager(workspace, {
      fetchImpl,
      maxConcurrent: 1,
    })
    managers.push(manager)

    const first = await manager.enqueue({ url: 'https://example.test/one', directoryPath: '', filename: 'one.txt' })
    const second = await manager.enqueue({ url: 'https://example.test/two', directoryPath: '', filename: 'two.txt' })
    await Bun.sleep(5)
    expect(manager.cancel(second.id)?.status).toBe('cancelled')
    releaseFirst?.()
    expect((await waitForTask(manager, first.id)).status).toBe('done')
    expect(fetchCalls).toBe(1)
  })
})
