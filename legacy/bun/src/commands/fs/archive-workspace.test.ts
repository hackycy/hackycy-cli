import type { ArchiveExtractor } from './archive-extractor'
import { mkdir, mkdtemp, readdir, readFile, rm, symlink, utimes, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { afterEach, describe, expect, test } from 'bun:test'
import { createFsWorkspace } from './workspace'

const temporaryDirectories: string[] = []

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function fixture(extractor: ArchiveExtractor): Promise<{ root: string, archive: string, workspace: Awaited<ReturnType<typeof createFsWorkspace>> }> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'ycy-archive-workspace-'))
  temporaryDirectories.push(root)
  const archive = path.join(root, 'backup.tar.gz')
  await writeFile(archive, 'fixture')
  return { root, archive, workspace: await createFsWorkspace(root, { archiveExtractor: extractor }) }
}

describe('FsWorkspace archive publication', () => {
  test('marks archives, publishes beside the source, and numbers directory collisions', async () => {
    const progress: number[] = []
    const { root, archive, workspace } = await fixture({
      async extract(_source, destination, options = {}) {
        options.onInspect?.({ uncompressedBytes: 7, entryCount: 1 })
        options.onProgress?.(50)
        await writeFile(path.join(destination, 'payload.txt'), 'payload')
        return { uncompressedBytes: 7, entryCount: 1 }
      },
    })
    await writeFile(path.join(root, 'notes.txt'), 'notes')

    const listing = await workspace.listDirectory('')
    expect(listing.entries.find(entry => entry.name === 'backup.tar.gz')?.extractable).toBe(true)
    expect(listing.entries.find(entry => entry.name === 'notes.txt')?.extractable).toBe(false)
    const first = await workspace.extractArchive('backup.tar.gz', { onProgress: value => progress.push(value) })
    const second = await workspace.extractArchive('backup.tar.gz')

    expect(first).toEqual({ archivePath: 'backup.tar.gz', destinationPath: 'backup', uncompressedBytes: 7, entryCount: 1 })
    expect(second.destinationPath).toBe('backup (1)')
    expect(progress).toEqual([50])
    expect(await readFile(archive, 'utf8')).toBe('fixture')
    expect(await readFile(path.join(root, 'backup', 'payload.txt'), 'utf8')).toBe('payload')
    expect(await readFile(path.join(root, 'backup (1)', 'payload.txt'), 'utf8')).toBe('payload')
  })

  test('hides staging directories, removes stale staging, and rolls back failures', async () => {
    const { root, workspace } = await fixture({
      async extract(_source, destination) {
        await writeFile(path.join(destination, 'partial.txt'), 'partial')
        throw new Error('extract failed')
      },
    })
    const stale = path.join(root, '.extract-11111111-1111-4111-8111-111111111111.tmp')
    const matchingFile = path.join(root, '.extract-22222222-2222-4222-8222-222222222222.tmp')
    await mkdir(stale)
    await writeFile(matchingFile, 'keep')
    const old = new Date(Date.now() - 25 * 60 * 60 * 1000)
    await utimes(stale, old, old)
    await utimes(matchingFile, old, old)

    expect((await workspace.listDirectory('')).entries.some(entry => entry.name.startsWith('.extract-'))).toBe(false)
    await expect(workspace.extractArchive('backup.tar.gz')).rejects.toMatchObject({
      code: 'UNAVAILABLE',
      message: 'Archive extraction failed',
    })
    expect((await readdir(root)).sort()).toEqual(['.extract-22222222-2222-4222-8222-222222222222.tmp', 'backup.tar.gz'])
    expect(await readFile(matchingFile, 'utf8')).toBe('keep')
  })

  test('rejects escaping symbolic links before publication', async () => {
    const { root, workspace } = await fixture({
      async extract(_source, destination) {
        await symlink('../../outside.txt', path.join(destination, 'escape'))
        return { uncompressedBytes: 0, entryCount: 1 }
      },
    })

    await expect(workspace.extractArchive('backup.tar.gz')).rejects.toMatchObject({ code: 'INVALID_ARCHIVE' })
    expect((await readdir(root)).sort()).toEqual(['backup.tar.gz'])
  })

  test('rejects broken symbolic links before publication', async () => {
    const { root, workspace } = await fixture({
      async extract(_source, destination) {
        await symlink('missing.txt', path.join(destination, 'broken'))
        return { uncompressedBytes: 0, entryCount: 1 }
      },
    })

    await expect(workspace.extractArchive('backup.tar.gz')).rejects.toMatchObject({ code: 'INVALID_ARCHIVE' })
    expect((await readdir(root)).sort()).toEqual(['backup.tar.gz'])
  })

  test('rejects special filesystem entries before publication', async () => {
    if (process.platform === 'win32')
      return
    const { root, workspace } = await fixture({
      async extract(_source, destination) {
        const fifo = path.join(destination, 'named-pipe')
        const child = Bun.spawn(['mkfifo', fifo], { stdin: 'ignore', stdout: 'ignore', stderr: 'pipe' })
        if (await child.exited !== 0)
          throw new Error(await new Response(child.stderr).text())
        return { uncompressedBytes: 0, entryCount: 1 }
      },
    })

    await expect(workspace.extractArchive('backup.tar.gz')).rejects.toMatchObject({ code: 'INVALID_ARCHIVE' })
    expect((await readdir(root)).sort()).toEqual(['backup.tar.gz'])
  })

  test('cancels extraction and removes all staging content', async () => {
    const controller = new AbortController()
    const { root, workspace } = await fixture({
      async extract(_source, destination, options = {}) {
        await writeFile(path.join(destination, 'partial.txt'), 'partial')
        return new Promise((_resolve, reject) => {
          options.signal?.addEventListener('abort', () => reject(options.signal?.reason), { once: true })
        })
      },
    })

    const extraction = workspace.extractArchive('backup.tar.gz', { signal: controller.signal })
    await Bun.sleep(5)
    controller.abort(new Error('cancelled'))
    await expect(extraction).rejects.toThrow('cancelled')
    expect((await readdir(root)).sort()).toEqual(['backup.tar.gz'])
  })

  test('does not accept a directory or unsupported file type as an archive', async () => {
    const { root, workspace } = await fixture({
      async extract() {
        return { uncompressedBytes: 0, entryCount: 0 }
      },
    })
    await mkdir(path.join(root, 'folder.zip'))
    await writeFile(path.join(root, 'notes.txt'), 'notes')

    await expect(workspace.extractArchive('folder.zip')).rejects.toMatchObject({ code: 'NOT_FILE' })
    await expect(workspace.extractArchive('notes.txt')).rejects.toMatchObject({ code: 'UNSUPPORTED_ARCHIVE' })
  })
})
