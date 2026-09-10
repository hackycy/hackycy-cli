import type { FsWorkspace } from './types'
import { describe, expect, test } from 'bun:test'
import { CHUNKED_UPLOAD_THRESHOLD_BYTES, ChunkedUploadError, createChunkedUploadManager } from './chunked-upload-service'

function fakeWorkspace(): { workspace: FsWorkspace, published: string[], aborted: number[] } {
  const published: string[] = []
  const aborted = [0]
  const workspace = {
    async beginChunkedUpload(_directory: string, filename: string, size: number) {
      return {
        async writeChunk(offset: number, stream: ReadableStream<Uint8Array>, expectedLength: number) {
          void offset
          void expectedLength
          await new Response(stream).arrayBuffer()
        },
        async publish() {
          published.push(filename)
          return { filename, path: filename, size }
        },
        async abort() {
          aborted[0] = aborted[0]! + 1
        },
      }
    },
  } as unknown as FsWorkspace
  return { workspace, published, aborted }
}

function blockingWorkspace(): { workspace: FsWorkspace, release: () => void, aborted: number[] } {
  let releaseWrite!: () => void
  const writeReleased = new Promise<void>((resolve) => {
    releaseWrite = resolve
  })
  const aborted = [0]
  const workspace = {
    async beginChunkedUpload(_directory: string, filename: string, size: number) {
      return {
        async writeChunk(_offset: number, stream: ReadableStream<Uint8Array>, _expectedLength: number, options?: { signal?: AbortSignal }) {
          const reader = stream.getReader()
          let rejectAbort!: (cause: unknown) => void
          const aborted = new Promise<never>((_, reject) => {
            rejectAbort = reject
          })
          const abort = (): void => {
            void reader.cancel()
            rejectAbort(options?.signal?.reason)
          }
          options?.signal?.addEventListener('abort', abort, { once: true })
          try {
            await Promise.race([writeReleased, aborted])
            await reader.cancel()
          }
          finally {
            options?.signal?.removeEventListener('abort', abort)
            reader.releaseLock()
          }
        },
        async publish() {
          return { filename, path: filename, size }
        },
        async abort() {
          aborted[0] = aborted[0]! + 1
        },
      }
    },
  } as unknown as FsWorkspace
  return { workspace, release: releaseWrite, aborted }
}

function part(text: string): ReadableStream<Uint8Array> {
  return new Response(text).body! as ReadableStream<Uint8Array>
}

describe('ChunkedUploadManager', () => {
  test('writes ordered chunks, rejects stale offsets, and completes idempotently', async () => {
    const fixture = fakeWorkspace()
    const size = CHUNKED_UPLOAD_THRESHOLD_BYTES + 4
    const manager = createChunkedUploadManager(fixture.workspace, { chunkSizeBytes: size })
    const created = await manager.create({ directoryPath: '', filename: 'large.bin', size }, 'owner')
    expect(created).toEqual(expect.objectContaining({ status: 'uploading', uploadedBytes: 0, chunkSizeBytes: size }))

    await manager.write(created.id, 'owner', { start: 0, end: 3, total: size }, part('abcd'))
    await expect(manager.write(created.id, 'owner', { start: 0, end: 3, total: size }, part('abcd'))).rejects.toMatchObject({
      code: 'CHUNKED_UPLOAD_OFFSET_MISMATCH',
    })
    await expect(manager.complete(created.id, 'owner')).rejects.toMatchObject({ code: 'CHUNKED_UPLOAD_INCOMPLETE' })
    await manager.write(created.id, 'owner', { start: 4, end: size - 1, total: size }, part('efgh'))
    const completed = await manager.complete(created.id, 'owner')
    expect(completed).toEqual(expect.objectContaining({ status: 'complete', uploadedBytes: size }))
    expect(fixture.published).toEqual(['large.bin'])
    expect(await manager.complete(created.id, 'owner')).toEqual(completed)
    await expect(manager.get(created.id, 'other')).rejects.toMatchObject({ code: 'CHUNKED_UPLOAD_NOT_FOUND' })
    await manager.close()
  })

  test('bounds active sessions per owner and removes cancelled staging', async () => {
    const fixture = fakeWorkspace()
    const manager = createChunkedUploadManager(fixture.workspace, { chunkSizeBytes: 4, maxSessionsPerOwner: 1 })
    const request = { directoryPath: '', filename: 'large.bin', size: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1 }
    const first = await manager.create(request, 'owner')
    await expect(manager.create({ ...request, filename: 'second.bin' }, 'owner')).rejects.toMatchObject({ code: 'CHUNKED_UPLOAD_LIMIT_REACHED' })
    await manager.cancel(first.id, 'owner')
    expect(fixture.aborted[0]).toBe(1)
    await manager.close()
  })

  test('rejects requests that do not exceed the chunking threshold', async () => {
    const fixture = fakeWorkspace()
    const manager = createChunkedUploadManager(fixture.workspace, { chunkSizeBytes: 4 })
    await expect(manager.create({ directoryPath: '', filename: 'small.bin', size: CHUNKED_UPLOAD_THRESHOLD_BYTES }, 'owner')).rejects.toEqual(
      new ChunkedUploadError('INVALID_CHUNKED_UPLOAD', 'Chunked uploads must exceed 20 MiB'),
    )
    await manager.close()
  })

  test('serializes concurrent completion retries and waits for an active completion during close', async () => {
    let publishCalls = 0
    let releasePublish!: () => void
    const publishReleased = new Promise<void>((resolve) => {
      releasePublish = resolve
    })
    const workspace = {
      async beginChunkedUpload(_directory: string, filename: string, size: number) {
        return {
          async writeChunk() {},
          async publish() {
            publishCalls++
            await publishReleased
            return { filename, path: filename, size }
          },
          async abort() {},
        }
      },
    } as unknown as FsWorkspace
    const manager = createChunkedUploadManager(workspace, { chunkSizeBytes: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1 })
    const size = CHUNKED_UPLOAD_THRESHOLD_BYTES + 1
    const created = await manager.create({ directoryPath: '', filename: 'large.bin', size }, 'owner')
    await manager.write(created.id, 'owner', { start: 0, end: size - 1, total: size }, new Response(new Uint8Array(size)).body!)

    const first = manager.complete(created.id, 'owner')
    const second = manager.complete(created.id, 'owner')
    await Bun.sleep(0)
    expect(publishCalls).toBe(1)
    const closing = manager.close()
    await Bun.sleep(0)
    releasePublish()
    const results = await Promise.all([first, second])
    expect(results[0]).toEqual(results[1])
    await closing
  })

  test('aborts a stalled chunk after the write timeout', async () => {
    const fixture = blockingWorkspace()
    const manager = createChunkedUploadManager(fixture.workspace, {
      chunkSizeBytes: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1,
      writeTimeoutMs: 5,
    })
    const size = CHUNKED_UPLOAD_THRESHOLD_BYTES + 1
    const created = await manager.create({ directoryPath: '', filename: 'large.bin', size }, 'owner')
    await expect(manager.write(created.id, 'owner', { start: 0, end: 0, total: size }, new ReadableStream())).rejects.toMatchObject({ code: 'CHUNKED_UPLOAD_TIMEOUT' })
    expect(fixture.aborted[0]).toBe(0)
    await manager.cancel(created.id, 'owner')
    fixture.release()
    await manager.close()
  })

  test('bounds terminal sessions by the global session limit', async () => {
    const fixture = fakeWorkspace()
    const manager = createChunkedUploadManager(fixture.workspace, {
      chunkSizeBytes: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1,
      maxSessions: 1,
    })
    const size = CHUNKED_UPLOAD_THRESHOLD_BYTES + 1
    const first = await manager.create({ directoryPath: '', filename: 'first.bin', size }, 'owner')
    await manager.write(first.id, 'owner', { start: 0, end: size - 1, total: size }, new Response(new Uint8Array(size)).body!)
    await manager.complete(first.id, 'owner')
    await expect(manager.create({ directoryPath: '', filename: 'second.bin', size }, 'owner')).rejects.toMatchObject({ code: 'CHUNKED_UPLOAD_LIMIT_REACHED' })
    await manager.close()
  })

  test('waits for a session creation to finish during close', async () => {
    let releaseCreate!: () => void
    const createReleased = new Promise<void>((resolve) => {
      releaseCreate = resolve
    })
    let aborted = 0
    const workspace = {
      async beginChunkedUpload() {
        await createReleased
        return {
          async writeChunk() {},
          async publish() { return { filename: 'large.bin', path: 'large.bin', size: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1 } },
          async abort() { aborted++ },
        }
      },
    } as unknown as FsWorkspace
    const manager = createChunkedUploadManager(workspace, { chunkSizeBytes: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1 })
    const creating = manager.create({ directoryPath: '', filename: 'large.bin', size: CHUNKED_UPLOAD_THRESHOLD_BYTES + 1 }, 'owner')
    await Bun.sleep(0)
    let closed = false
    const closing = manager.close().then(() => {
      closed = true
    })
    await Bun.sleep(0)
    expect(closed).toBe(false)
    releaseCreate()
    await expect(creating).rejects.toMatchObject({ code: 'CHUNKED_UPLOAD_STOPPED' })
    await closing
    expect(aborted).toBe(1)
  })
})
