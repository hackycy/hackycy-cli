import type { StatsFs } from 'node:fs'
import type { FsWorkspace } from './types'
import { chmod, lstat, mkdir, mkdtemp, readdir, readFile, readlink, rm, stat, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { createFsWorkspace, MAX_TEXT_PREVIEW_BYTES } from './workspace'

const temporaryDirectories: string[] = []

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function createFixture(): Promise<{ root: string, workspace: FsWorkspace }> {
  const fixture = await mkdtemp(path.join(tmpdir(), 'ycy-fs-workspace-'))
  temporaryDirectories.push(fixture)
  const root = path.join(fixture, 'shared files')
  await mkdir(path.join(root, 'docs'), { recursive: true })
  await Promise.all([
    writeFile(path.join(root, 'zeta.txt'), 'zeta'),
    writeFile(path.join(root, 'Alpha.txt'), 'alpha'),
  ])
  return { root, workspace: await createFsWorkspace(root) }
}

describe('FsWorkspace', () => {
  test('lists one directory with stable browser paths and directories first', async () => {
    const { workspace } = await createFixture()

    const listing = await workspace.listDirectory('')

    expect(listing).toEqual({
      rootName: 'shared files',
      path: '',
      parentPath: undefined,
      entries: [
        expect.objectContaining({
          name: 'docs',
          path: 'docs',
          kind: 'directory',
          browseUrl: '/browse/docs',
        }),
        expect.objectContaining({
          name: 'Alpha.txt',
          path: 'Alpha.txt',
          kind: 'file',
          fileUrl: '/files/Alpha.txt',
          downloadUrl: '/files/Alpha.txt?download=1',
        }),
        expect.objectContaining({
          name: 'zeta.txt',
          path: 'zeta.txt',
          kind: 'file',
          fileUrl: '/files/zeta.txt',
          downloadUrl: '/files/zeta.txt?download=1',
        }),
      ],
    })
  })

  test('accepts only root-confined POSIX paths and marks escaping symlinks unavailable', async () => {
    const { root, workspace } = await createFixture()
    const outside = path.join(path.dirname(root), 'outside')
    await mkdir(outside)
    await writeFile(path.join(outside, 'secret.txt'), 'secret')
    await Promise.all([
      symlink(path.join(root, 'docs'), path.join(root, 'internal-docs')),
      symlink(outside, path.join(root, 'external-docs')),
    ])

    const listing = await workspace.listDirectory('')

    expect(listing.entries.find(entry => entry.name === 'internal-docs')).toEqual(expect.objectContaining({
      kind: 'directory',
      isSymlink: true,
      browseUrl: '/browse/internal-docs',
    }))
    expect(listing.entries.find(entry => entry.name === 'external-docs')).toEqual(expect.objectContaining({
      kind: 'unavailable',
      isSymlink: true,
    }))
    await expect(workspace.listDirectory('../outside')).rejects.toMatchObject({ code: 'PATH_FORBIDDEN' })
    await expect(workspace.listDirectory('docs\\..\\outside')).rejects.toMatchObject({ code: 'INVALID_PATH' })
    await expect(workspace.listDirectory('external-docs')).rejects.toMatchObject({ code: 'PATH_FORBIDDEN' })
  })

  test('rejects NUL bytes before passing paths or filenames to the filesystem', async () => {
    const { workspace } = await createFixture()

    await expect(workspace.listDirectory('\0')).rejects.toMatchObject({ code: 'INVALID_PATH' })
    await expect(workspace.uploadFile('docs', new File(['bad'], 'bad\0name.txt'))).rejects.toMatchObject({ code: 'INVALID_UPLOAD' })
  })

  test('opens a root-confined file without exposing its absolute path', async () => {
    const { workspace } = await createFixture()

    const file = await workspace.openFile('Alpha.txt')

    expect(file).toEqual(expect.objectContaining({
      name: 'Alpha.txt',
      size: 5,
      mimeType: 'text/plain;charset=utf-8',
    }))
    expect(await file.body.text()).toBe('alpha')
    await expect(workspace.openFile('docs')).rejects.toMatchObject({ code: 'NOT_FILE' })
  })

  test('classifies structured text MIME types with parameters as text previews', async () => {
    const { root, workspace } = await createFixture()
    await writeFile(path.join(root, 'data.json'), '{"ready":true}')

    const listing = await workspace.listDirectory('')

    expect(listing.entries.find(entry => entry.name === 'data.json')).toEqual(expect.objectContaining({
      mimeType: 'application/json;charset=utf-8',
      previewKind: 'text',
      syntaxLanguage: 'json',
    }))
  })

  test('classifies source code and dotenv variants by filename before MIME type', async () => {
    const { root, workspace } = await createFixture()
    const files = new Map([
      ['app.ts', 'typescript'],
      ['component.tsx', 'tsx'],
      ['main.py', 'python'],
      ['Dockerfile', 'dockerfile'],
      ['.env', 'dotenv'],
      ['.env.local', 'dotenv'],
      ['service.env', 'dotenv'],
    ])
    await Promise.all([...files].map(([name]) => writeFile(path.join(root, name), 'VALUE=true')))

    const listing = await workspace.listDirectory('')

    for (const [name, language] of files) {
      expect(listing.entries.find(entry => entry.name === name)).toEqual(expect.objectContaining({
        previewKind: 'text',
        syntaxLanguage: language,
      }))
    }
  })

  test('returns bounded decoded text without loading oversized or binary files as text', async () => {
    const { root, workspace } = await createFixture()
    await Promise.all([
      writeFile(path.join(root, 'utf16.txt'), Uint8Array.from([0xFF, 0xFE, 0x68, 0x00, 0x69, 0x00])),
      writeFile(path.join(root, 'binary.txt'), Uint8Array.from([0xC3, 0x28])),
      writeFile(path.join(root, 'nul.txt'), Uint8Array.from([0x00, 0x01, 0x02])),
      writeFile(path.join(root, 'max.txt'), new Uint8Array(10 * 1024 * 1024).fill(0x61)),
      writeFile(path.join(root, 'large.txt'), new Uint8Array(10 * 1024 * 1024 + 1)),
    ])

    expect(await workspace.readTextPreview('Alpha.txt')).toEqual(expect.objectContaining({
      status: 'ready',
      text: 'alpha',
      encoding: 'utf-8',
      size: 5,
      revision: expect.stringMatching(/^[0-9a-f]{64}$/),
    }))
    expect(await workspace.readTextPreview('utf16.txt')).toEqual(expect.objectContaining({
      status: 'ready',
      text: 'hi',
      encoding: 'utf-16le',
      size: 6,
      revision: expect.stringMatching(/^[0-9a-f]{64}$/),
    }))
    expect(await workspace.readTextPreview('binary.txt')).toEqual({ status: 'binary', size: 2 })
    expect(await workspace.readTextPreview('nul.txt')).toEqual(expect.objectContaining({
      status: 'ready',
      text: '\0\u0001\u0002',
      encoding: 'utf-8',
      size: 3,
      revision: expect.stringMatching(/^[0-9a-f]{64}$/),
    }))
    const max = await workspace.readTextPreview('max.txt')
    expect(max).toEqual(expect.objectContaining({ status: 'ready', size: 10 * 1024 * 1024, encoding: 'utf-8' }))
    if (max.status === 'ready')
      expect(max.text.length).toBe(10 * 1024 * 1024)
    expect(await workspace.readTextPreview('large.txt')).toEqual({
      status: 'too_large',
      size: 10 * 1024 * 1024 + 1,
      maxBytes: 10 * 1024 * 1024,
    })
  })

  test('discovers extensionless text by content without listing it as text', async () => {
    const { root, workspace } = await createFixture()
    await writeFile(path.join(root, '.claude'), 'model=local')

    expect((await workspace.listDirectory('')).entries.find(entry => entry.name === '.claude')).toEqual(expect.objectContaining({ previewKind: 'none' }))
    expect(await workspace.readTextPreview('.claude')).toEqual(expect.objectContaining({
      status: 'ready',
      text: 'model=local',
      encoding: 'utf-8',
    }))
  })

  test('uploads atomically and chooses a new name instead of overwriting', async () => {
    const { workspace } = await createFixture()

    const first = await workspace.uploadFile('docs', new File(['first'], 'report.txt'))
    const second = await workspace.uploadFile('docs', new File(['second'], 'report.txt'))

    expect(first).toEqual({ filename: 'report.txt', path: 'docs/report.txt', size: 5 })
    expect(second).toEqual({ filename: 'report (1).txt', path: 'docs/report (1).txt', size: 6 })
    expect(await (await workspace.openFile(first.path)).body.text()).toBe('first')
    expect(await (await workspace.openFile(second.path)).body.text()).toBe('second')
    expect((await workspace.listDirectory('docs')).entries.map(entry => entry.name)).toEqual([
      'report (1).txt',
      'report.txt',
    ])
    await expect(workspace.uploadFile('docs', new File(['bad'], '../secret.txt'))).rejects.toMatchObject({ code: 'INVALID_UPLOAD' })
  })

  test('writes chunked upload staging at explicit offsets and publishes only when complete', async () => {
    const { workspace, root } = await createFixture()
    const upload = await workspace.beginChunkedUpload('docs', 'chunked.bin', 5)
    await upload.writeChunk(0, new Response('abc').body!, 3)
    expect((await workspace.listDirectory('docs')).entries).toHaveLength(0)
    await upload.writeChunk(3, new Response('de').body!, 2)
    expect(await upload.publish()).toEqual({ filename: 'chunked.bin', path: 'docs/chunked.bin', size: 5 })
    expect(await readFile(path.join(root, 'docs', 'chunked.bin'), 'utf8')).toBe('abcde')
    await upload.abort()
    expect((await readdir(path.join(root, 'docs'))).some(name => name.startsWith('.upload-'))).toBe(false)
  })

  test('publishes one result when completion is retried concurrently', async () => {
    const { workspace, root } = await createFixture()
    const upload = await workspace.beginChunkedUpload('docs', 'concurrent.bin', 3)
    await upload.writeChunk(0, new Response('abc').body!, 3)

    const results = await Promise.all([upload.publish(), upload.publish()])

    expect(results[0]).toEqual(results[1])
    expect((await readdir(path.join(root, 'docs'))).filter(name => name.startsWith('concurrent'))).toEqual(['concurrent.bin'])
  })

  test('reserves capacity for active chunked uploads before accepting another session', async () => {
    const { root } = await createFixture()
    const workspace = await createFsWorkspace(root, {
      statfs: async () => ({ bsize: 1, bavail: 100, bfree: 100, blocks: 100, ffree: 100, files: 100, type: 0 }) as StatsFs,
    })
    const first = await workspace.beginChunkedUpload('docs', 'first.bin', 60)
    await expect(workspace.beginChunkedUpload('docs', 'second.bin', 31)).rejects.toMatchObject({ code: 'INSUFFICIENT_SPACE' })
    await first.abort()
    const second = await workspace.beginChunkedUpload('docs', 'second.bin', 31)
    await second.abort()
  })

  test('serializes concurrent capacity checks before reserving upload space', async () => {
    const { root } = await createFixture()
    let releaseStatfs!: () => void
    const statfsReleased = new Promise<void>((resolve) => {
      releaseStatfs = resolve
    })
    let calls = 0
    const workspace = await createFsWorkspace(root, {
      statfs: async () => {
        calls++
        if (calls === 2)
          releaseStatfs()
        await statfsReleased
        return { bsize: 1, bavail: 100, bfree: 100, blocks: 100, ffree: 100, files: 100, type: 0 } as StatsFs
      },
    })
    const first = workspace.beginChunkedUpload('docs', 'first.bin', 60)
    const second = workspace.beginChunkedUpload('docs', 'second.bin', 60)
    releaseStatfs()
    const results = await Promise.allSettled([first, second])
    expect(results.filter(result => result.status === 'fulfilled')).toHaveLength(1)
    expect(results.filter(result => result.status === 'rejected')).toHaveLength(1)
  })

  test('conditionally saves text while preserving encoding, BOM, line endings, and mode bits', async () => {
    const { root, workspace } = await createFixture()
    const target = path.join(root, 'edited.txt')
    await writeFile(target, 'one\r\ntwo\r\n')
    await chmod(target, 0o640)

    const preview = await workspace.readTextPreview('edited.txt')
    expect(preview.status).toBe('ready')
    if (preview.status !== 'ready')
      return
    const saved = await workspace.saveTextFile('edited.txt', 'new\ncontent', preview.revision)
    expect(saved).toEqual(expect.objectContaining({
      encoding: 'utf-8',
      size: 'new\r\ncontent\r\n'.length,
      revision: expect.stringMatching(/^[0-9a-f]{64}$/),
    }))
    expect(await readFile(target, 'utf8')).toBe('new\r\ncontent\r\n')
    expect((await stat(target)).mode & 0o7777).toBe(0o640)

    const stale = await workspace.readTextPreview('edited.txt')
    await expect(workspace.saveTextFile('edited.txt', 'stale', preview.revision)).rejects.toMatchObject({ code: 'REVISION_MISMATCH' })
    expect(stale.status).toBe('ready')
  })

  test('rejects edited text over the 10 MiB limit', async () => {
    const { workspace } = await createFixture()
    const preview = await workspace.readTextPreview('Alpha.txt')
    expect(preview.status).toBe('ready')
    if (preview.status !== 'ready')
      return

    await expect(workspace.saveTextFile('Alpha.txt', 'x'.repeat(MAX_TEXT_PREVIEW_BYTES + 1), preview.revision)).rejects.toMatchObject({
      code: 'TOO_LARGE',
    })
  })

  test('preserves UTF-16 BOMs and rejects final symbolic links as edit targets', async () => {
    const { root, workspace } = await createFixture()
    await writeFile(path.join(root, 'be.txt'), Uint8Array.from([0xFE, 0xFF, 0x00, 0x68, 0x00, 0x69]))
    await writeFile(path.join(root, 'utf8-bom.txt'), Uint8Array.from([0xEF, 0xBB, 0xBF, 0x68, 0x69]))
    await symlink('Alpha.txt', path.join(root, 'linked.txt'))

    const preview = await workspace.readTextPreview('be.txt')
    expect(preview.status).toBe('ready')
    if (preview.status === 'ready') {
      await workspace.saveTextFile('be.txt', 'ok', preview.revision)
      expect(Array.from(await readFile(path.join(root, 'be.txt')))).toEqual([0xFE, 0xFF, 0x00, 0x6F, 0x00, 0x6B])
    }
    const utf8Bom = await workspace.readTextPreview('utf8-bom.txt')
    expect(utf8Bom.status).toBe('ready')
    if (utf8Bom.status === 'ready') {
      await workspace.saveTextFile('utf8-bom.txt', 'ok', utf8Bom.revision)
      expect(Array.from(await readFile(path.join(root, 'utf8-bom.txt')))).toEqual([0xEF, 0xBB, 0xBF, 0x6F, 0x6B])
    }
    const linked = await workspace.readTextPreview('linked.txt')
    expect(linked.status).toBe('ready')
    if (linked.status === 'ready')
      await expect(workspace.saveTextFile('linked.txt', 'changed', linked.revision)).rejects.toMatchObject({ code: 'NOT_FILE' })
  })

  test('writes a streamed download atomically with bounded progress updates', async () => {
    const { root, workspace } = await createFixture()
    const progress: number[] = []
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(Uint8Array.from([1, 2]))
        controller.enqueue(Uint8Array.from([3, 4, 5]))
        controller.close()
      },
    })

    const first = await workspace.writeFileStream('docs', 'remote.bin', stream, {
      onProgress: bytes => progress.push(bytes),
    })
    const second = await workspace.writeFileStream('docs', 'remote.bin', new Response('again').body!)

    expect(first).toEqual({ filename: 'remote.bin', path: 'docs/remote.bin', size: 5 })
    expect(second.filename).toBe('remote (1).bin')
    expect(progress).toEqual([2, 5])
    expect(Array.from(new Uint8Array(await (await workspace.openFile(first.path)).body.arrayBuffer()))).toEqual([1, 2, 3, 4, 5])
    expect((await workspace.listDirectory('docs')).entries.map(entry => entry.name)).toEqual(['remote (1).bin', 'remote.bin'])
    expect((await readdir(path.join(root, 'docs'))).some(name => name.startsWith('.download-'))).toBe(false)
  })

  test('cancels streamed writes without publishing a partial file', async () => {
    const { root, workspace } = await createFixture()
    const controller = new AbortController()
    controller.abort(new Error('cancelled'))

    await expect(workspace.writeFileStream('docs', 'cancelled.bin', new Response('partial').body!, { signal: controller.signal })).rejects.toThrow('cancelled')
    expect((await workspace.listDirectory('docs')).entries).toHaveLength(0)
    expect((await readdir(path.join(root, 'docs'))).some(name => name.startsWith('.download-'))).toBe(false)
  })

  test('creates a directory without overwriting an existing entry', async () => {
    const { workspace } = await createFixture()

    expect(await workspace.applyOperation({ action: 'create-directory', parentPath: '', name: 'projects' })).toEqual({
      action: 'create-directory',
      items: [{ status: 'ok', destinationPath: 'projects' }],
    })
    expect((await workspace.listDirectory('')).entries.map(entry => entry.name)).toContain('projects')

    expect(await workspace.applyOperation({ action: 'create-directory', parentPath: '', name: 'projects' })).toEqual({
      action: 'create-directory',
      items: [{
        status: 'error',
        destinationPath: 'projects',
        error: { code: 'ALREADY_EXISTS', message: 'An entry with that name already exists' },
      }],
    })
  })

  test('rejects invalid names for create and rename operations', async () => {
    const { workspace } = await createFixture()

    for (const name of ['', '   ', '.', '..', 'nested/name', 'nested\\name', 'nul\0name']) {
      expect(await workspace.applyOperation({ action: 'create-directory', parentPath: '', name })).toEqual({
        action: 'create-directory',
        items: [{
          status: 'error',
          error: { code: 'INVALID_NAME', message: 'Entry name is invalid' },
        }],
      })
    }

    expect(await workspace.applyOperation({ action: 'rename', path: 'Alpha.txt', newName: '../renamed.txt' })).toEqual({
      action: 'rename',
      items: [{
        status: 'error',
        sourcePath: 'Alpha.txt',
        error: { code: 'INVALID_NAME', message: 'Entry name is invalid' },
      }],
    })
  })

  test('renames an entry without overwriting another entry', async () => {
    const { workspace } = await createFixture()

    expect(await workspace.applyOperation({ action: 'rename', path: 'Alpha.txt', newName: 'beta.txt' })).toEqual({
      action: 'rename',
      items: [{ status: 'ok', sourcePath: 'Alpha.txt', destinationPath: 'beta.txt' }],
    })
    expect(await (await workspace.openFile('beta.txt')).body.text()).toBe('alpha')

    expect(await workspace.applyOperation({ action: 'rename', path: 'zeta.txt', newName: 'beta.txt' })).toEqual({
      action: 'rename',
      items: [{
        status: 'error',
        sourcePath: 'zeta.txt',
        destinationPath: 'beta.txt',
        error: { code: 'ALREADY_EXISTS', message: 'An entry with that name already exists' },
      }],
    })
  })

  test('copies files and directories recursively with collision-safe names', async () => {
    const { root, workspace } = await createFixture()
    await writeFile(path.join(root, 'docs', 'guide.txt'), 'guide')

    expect(await workspace.applyOperation({ action: 'copy', paths: ['Alpha.txt', 'docs'], destinationPath: 'docs' })).toEqual({
      action: 'copy',
      items: [
        { status: 'ok', sourcePath: 'Alpha.txt', destinationPath: 'docs/Alpha.txt' },
        { status: 'error', sourcePath: 'docs', error: { code: 'INVALID_OPERATION', message: 'A directory cannot be copied into itself' } },
      ],
    })
    expect(await (await workspace.openFile('docs/Alpha.txt')).body.text()).toBe('alpha')

    expect(await workspace.applyOperation({ action: 'copy', paths: ['docs'], destinationPath: '' })).toEqual({
      action: 'copy',
      items: [{ status: 'ok', sourcePath: 'docs', destinationPath: 'docs (1)' }],
    })
    expect(await (await workspace.openFile('docs (1)/guide.txt')).body.text()).toBe('guide')
  })

  test('copies symlinks without dereferencing them', async () => {
    const { root, workspace } = await createFixture()
    const outside = path.join(path.dirname(root), 'outside-copy-target')
    await mkdir(outside)
    await writeFile(path.join(outside, 'keep.txt'), 'keep')
    await Promise.all([
      symlink('../Alpha.txt', path.join(root, 'docs', 'alpha-link')),
      symlink(outside, path.join(root, 'docs', 'outside-link')),
    ])

    expect(await workspace.applyOperation({ action: 'copy', paths: ['docs'], destinationPath: '' })).toEqual({
      action: 'copy',
      items: [{ status: 'ok', sourcePath: 'docs', destinationPath: 'docs (1)' }],
    })
    expect((await lstat(path.join(root, 'docs (1)', 'alpha-link'))).isSymbolicLink()).toBe(true)
    expect((await lstat(path.join(root, 'docs (1)', 'outside-link'))).isSymbolicLink()).toBe(true)
    expect(await readlink(path.join(root, 'docs (1)', 'alpha-link'))).toBe('../Alpha.txt')
    expect(await readlink(path.join(root, 'docs (1)', 'outside-link'))).toBe(outside)
    expect(await Bun.file(path.join(outside, 'keep.txt')).text()).toBe('keep')
  })

  test('rejects mutations through a symlink ancestor that escapes the root', async () => {
    const { root, workspace } = await createFixture()
    const outside = path.join(path.dirname(root), 'outside-operation-target')
    await mkdir(outside)
    await writeFile(path.join(outside, 'secret.txt'), 'secret')
    await symlink(outside, path.join(root, 'escape'))

    expect(await workspace.applyOperation({ action: 'create-directory', parentPath: 'escape', name: 'created' })).toEqual({
      action: 'create-directory',
      items: [{ status: 'error', error: { code: 'PATH_FORBIDDEN', message: 'Path escapes the file browser directory' } }],
    })
    expect(await workspace.applyOperation({ action: 'rename', path: 'escape/secret.txt', newName: 'renamed.txt' })).toEqual({
      action: 'rename',
      items: [{ status: 'error', sourcePath: 'escape/secret.txt', error: { code: 'PATH_FORBIDDEN', message: 'Path escapes the file browser directory' } }],
    })
    expect(await workspace.applyOperation({ action: 'copy', paths: ['Alpha.txt'], destinationPath: 'escape' })).toEqual({
      action: 'copy',
      items: [{ status: 'error', sourcePath: 'Alpha.txt', error: { code: 'PATH_FORBIDDEN', message: 'Path escapes the file browser directory' } }],
    })
    expect(await workspace.applyOperation({ action: 'move', paths: ['Alpha.txt'], destinationPath: 'escape' })).toEqual({
      action: 'move',
      items: [{ status: 'error', sourcePath: 'Alpha.txt', error: { code: 'PATH_FORBIDDEN', message: 'Path escapes the file browser directory' } }],
    })
    expect(await workspace.applyOperation({ action: 'delete', paths: ['escape/secret.txt'] })).toEqual({
      action: 'delete',
      items: [{ status: 'error', sourcePath: 'escape/secret.txt', error: { code: 'PATH_FORBIDDEN', message: 'Path escapes the file browser directory' } }],
    })
    expect(await Bun.file(path.join(outside, 'secret.txt')).text()).toBe('secret')
  })

  test('moves entries without overwriting or moving a directory into itself', async () => {
    const { root, workspace } = await createFixture()
    await writeFile(path.join(root, 'docs', 'zeta.txt'), 'occupied')

    expect(await workspace.applyOperation({ action: 'move', paths: ['Alpha.txt', 'zeta.txt', 'docs'], destinationPath: 'docs' })).toEqual({
      action: 'move',
      items: [
        { status: 'ok', sourcePath: 'Alpha.txt', destinationPath: 'docs/Alpha.txt' },
        {
          status: 'error',
          sourcePath: 'zeta.txt',
          destinationPath: 'docs/zeta.txt',
          error: { code: 'ALREADY_EXISTS', message: 'An entry with that name already exists' },
        },
        {
          status: 'error',
          sourcePath: 'docs',
          error: { code: 'INVALID_OPERATION', message: 'A directory cannot be moved into itself' },
        },
      ],
    })
    expect(await (await workspace.openFile('docs/Alpha.txt')).body.text()).toBe('alpha')
    await expect(workspace.openFile('Alpha.txt')).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  test('permanently deletes entries without following final symlinks and reports partial failures', async () => {
    const { root, workspace } = await createFixture()
    const outside = path.join(path.dirname(root), 'outside-delete-target')
    await mkdir(outside)
    await writeFile(path.join(outside, 'keep.txt'), 'keep')
    await symlink(outside, path.join(root, 'outside-link'))

    expect(await workspace.applyOperation({ action: 'delete', paths: ['Alpha.txt', 'docs', 'outside-link', 'missing.txt', ''] })).toEqual({
      action: 'delete',
      items: [
        { status: 'ok', sourcePath: 'Alpha.txt' },
        { status: 'ok', sourcePath: 'docs' },
        { status: 'ok', sourcePath: 'outside-link' },
        { status: 'error', sourcePath: 'missing.txt', error: { code: 'NOT_FOUND', message: 'Path does not exist' } },
        { status: 'error', sourcePath: '', error: { code: 'ROOT_IMMUTABLE', message: 'The file browser root cannot be changed' } },
      ],
    })
    expect((await workspace.listDirectory('')).entries.map(entry => entry.name)).toEqual(['zeta.txt'])
    expect(await Bun.file(path.join(outside, 'keep.txt')).text()).toBe('keep')
  })
})
