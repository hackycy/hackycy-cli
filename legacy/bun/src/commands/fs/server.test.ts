import type { RunningFsServer } from './server'
import type { ThumbnailWorkerRequest } from './thumbnail-worker'
import type { FsDownloadManager, FsExtractionManager } from './types'
import { Buffer } from 'node:buffer'
import { mkdtemp, readdir, rm, truncate, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { createFsAuthentication } from './authentication'
import { createRemoteDownloadManager } from './download-service'
import { createExtractionManager } from './extraction-service'
import { startFsHttpServer } from './server'
import { THUMBNAIL_MAX_INPUT_BYTES, ThumbnailService } from './thumbnail-service'
import { createFsWorkspace, MAX_TEXT_PREVIEW_BYTES, MAX_UPLOAD_BYTES } from './workspace'

const temporaryDirectories: string[] = []
const servers: RunningFsServer[] = []

async function createSessionDirectory(): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), 'ycy-fs-session-'))
  temporaryDirectories.push(directory)
  return directory
}

function onePixelPng(): Buffer {
  return Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
}

function fakeThumbnailWorker(handle: (worker: Worker, request: ThumbnailWorkerRequest) => void): Worker {
  const worker = {
    onerror: null,
    onmessage: null,
    postMessage(request: ThumbnailWorkerRequest) {
      handle(worker as unknown as Worker, request)
    },
    terminate() {},
  }
  return worker as unknown as Worker
}

afterEach(async () => {
  await Promise.all(servers.splice(0).map(server => server.stop()))
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function startFixtureServer(
  managementEnabled = false,
  createThumbnailService?: (workspace: Awaited<ReturnType<typeof createFsWorkspace>>) => ThumbnailService,
  createDownloadManager?: (workspace: Awaited<ReturnType<typeof createFsWorkspace>>) => FsDownloadManager,
  authentication?: Awaited<ReturnType<typeof createFsAuthentication>>,
  createArchiveExtractionManager?: (workspace: Awaited<ReturnType<typeof createFsWorkspace>>) => FsExtractionManager,
  safeHtml = false,
  chunkedUpload = false,
): Promise<{ server: RunningFsServer, root: string }> {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-fs-http-'))
  temporaryDirectories.push(root)
  await writeFile(path.join(root, 'hello.txt'), 'hello world')
  const workspace = await createFsWorkspace(root)
  const server = startFsHttpServer({
    workspace,
    address: '127.0.0.1',
    port: 0,
    managementEnabled,
    safeHtml,
    authentication,
    thumbnailService: createThumbnailService?.(workspace),
    downloadManager: createDownloadManager?.(workspace),
    extractionManager: createArchiveExtractionManager?.(workspace),
    chunkedUpload: chunkedUpload ? { enabled: true, chunkSizeBytes: 8 * 1024 * 1024 } : undefined,
  })
  servers.push(server)
  return { server, root }
}

async function login(server: RunningFsServer, username = 'alice', password = 'password123'): Promise<{ response: Response, cookie?: string }> {
  const response = await fetch(new URL('/api/session', server.url), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Origin': server.url.origin },
    body: JSON.stringify({ username, password }),
  })
  return { response, cookie: response.headers.get('set-cookie')?.split(';')[0] }
}

describe('FsHttpServer', () => {
  test('reports disabled authentication without changing existing access', async () => {
    const { server } = await startFixtureServer()

    const session = await fetch(new URL('/api/session', server.url))
    const directory = await fetch(new URL('/api/directory', server.url))

    expect(await session.json()).toEqual({ version: 1, authenticationEnabled: false })
    expect(directory.status).toBe(200)
  })

  test('requires a valid account session for every file and data route', async () => {
    const authentication = await createFsAuthentication(['Alice:password123'], { directory: await createSessionDirectory() })
    const { server } = await startFixtureServer(true, undefined, undefined, authentication)
    const protectedRequests = [
      fetch(new URL('/api/directory', server.url)),
      fetch(new URL('/api/text?path=hello.txt', server.url)),
      fetch(new URL('/api/upload', server.url), { method: 'POST' }),
      fetch(new URL('/api/uploads', server.url), { method: 'POST' }),
      fetch(new URL('/api/uploads/00000000-0000-0000-0000-000000000000', server.url)),
      fetch(new URL('/api/operations', server.url), { method: 'POST' }),
      fetch(new URL('/api/downloads', server.url)),
      fetch(new URL('/api/downloads/events', server.url)),
      fetch(new URL('/api/extractions', server.url)),
      fetch(new URL('/api/extractions/events', server.url)),
      fetch(new URL('/files/hello.txt', server.url)),
      fetch(new URL('/thumbnails/hello.txt', server.url)),
    ]

    expect((await fetch(server.url)).status).toBe(200)
    expect((await fetch(new URL('/browse/private', server.url))).status).toBe(200)
    for (const response of await Promise.all(protectedRequests)) {
      expect(response.status).toBe(401)
      expect(await response.json()).toEqual({
        version: 1,
        error: { code: 'AUTHENTICATION_REQUIRED', message: 'Authenticated session is required' },
      })
    }

    const missingOrigin = await fetch(new URL('/api/session', server.url), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'alice', password: 'password123' }),
    })
    expect(missingOrigin.status).toBe(403)

    const rejected = await login(server, 'alice', 'wrong-password')
    expect(rejected.response.status).toBe(401)
    expect(await rejected.response.json()).toEqual({
      version: 1,
      error: { code: 'AUTHENTICATION_FAILED', message: 'Account credentials are invalid' },
    })

    const signedIn = await login(server, 'ALICE')
    expect(signedIn.response.status).toBe(200)
    expect(signedIn.cookie).toStartWith('ycy_fs_session=')
    expect(signedIn.response.headers.get('set-cookie')).toContain('HttpOnly; SameSite=Strict; Path=/; Max-Age=604800')

    const session = await fetch(new URL('/api/session', server.url), { headers: { Cookie: signedIn.cookie! } })
    expect(await session.json()).toEqual({
      version: 1,
      authenticationEnabled: true,
      authenticated: true,
      account: { username: 'Alice' },
    })
    const directory = await fetch(new URL('/api/directory', server.url), { headers: { Cookie: signedIn.cookie! } })
    expect(directory.status).toBe(200)
    expect(directory.headers.get('set-cookie')).toContain('Max-Age=604800')

    const file = await fetch(new URL('/files/hello.txt', server.url), { headers: { Cookie: signedIn.cookie! } })
    expect(file.status).toBe(200)
    expect(await file.text()).toBe('hello world')
    expect(file.headers.get('access-control-allow-origin')).toBeNull()
    const preflight = await fetch(new URL('/files/hello.txt', server.url), { method: 'OPTIONS', headers: { Cookie: signedIn.cookie! } })
    expect(preflight.status).toBe(204)
    expect(preflight.headers.get('access-control-allow-origin')).toBeNull()

    const signedOut = await fetch(new URL('/api/session', server.url), {
      method: 'DELETE',
      headers: { Cookie: signedIn.cookie!, Origin: server.url.origin },
    })
    expect(signedOut.status).toBe(204)
    expect(signedOut.headers.get('set-cookie')).toContain('Max-Age=0')
    expect((await fetch(new URL('/api/directory', server.url), { headers: { Cookie: signedIn.cookie! } })).status).toBe(401)
  })

  test('closes an authenticated download event stream when its session expires', async () => {
    const authentication = await createFsAuthentication(['alice:password123'], { directory: await createSessionDirectory(), sessionLifetimeMs: 40 })
    const { server } = await startFixtureServer(true, undefined, undefined, authentication)
    const signedIn = await login(server)
    const response = await fetch(new URL('/api/downloads/events', server.url), { headers: { Cookie: signedIn.cookie! } })
    const reader = response.body!.getReader()

    expect(response.status).toBe(200)
    expect((await reader.read()).done).toBe(false)
    expect((await reader.read()).done).toBe(true)
  })

  test('returns a storage error instead of renewing an undurable authenticated session', async () => {
    const directory = await createSessionDirectory()
    const authentication = await createFsAuthentication(['alice:password123'], { directory })
    const { server } = await startFixtureServer(true, undefined, undefined, authentication)
    const signedIn = await login(server)
    await rm(directory, { recursive: true, force: true })
    await writeFile(directory, 'unavailable')

    const response = await fetch(new URL('/api/directory', server.url), { headers: { Cookie: signedIn.cookie! } })

    expect(response.status).toBe(503)
    expect(await response.json()).toEqual({
      version: 1,
      error: { code: 'SESSION_UNAVAILABLE', message: 'Session storage is unavailable' },
    })
  })

  test('serves a versioned directory listing with API security headers', async () => {
    const { server, root } = await startFixtureServer()

    const response = await fetch(new URL('/api/directory?path=', server.url))

    expect(response.status).toBe(200)
    expect(await response.json()).toEqual({
      version: 1,
      rootName: path.basename(root),
      path: '',
      managementEnabled: false,
      maxUploadBytes: MAX_UPLOAD_BYTES,
      entries: [expect.objectContaining({ name: 'hello.txt', path: 'hello.txt' })],
    })
    expect(response.headers.get('cache-control')).toBe('no-store')
    expect(response.headers.get('x-content-type-options')).toBe('nosniff')
    expect(response.headers.get('referrer-policy')).toBe('no-referrer')
    expect(response.headers.get('access-control-allow-origin')).toBeNull()
  })

  test('exposes syntax language metadata for code and dotenv previews', async () => {
    const { server, root } = await startFixtureServer()
    await Promise.all([
      writeFile(path.join(root, 'app.ts'), 'export const ready = true'),
      writeFile(path.join(root, '.env.production'), 'READY=true'),
    ])

    const response = await fetch(new URL('/api/directory?path=', server.url))
    const listing = await response.json() as { entries: Array<{ name: string, previewKind: string, syntaxLanguage?: string }> }

    expect(listing.entries.find(entry => entry.name === 'app.ts')).toEqual(expect.objectContaining({
      previewKind: 'text',
      syntaxLanguage: 'typescript',
    }))
    expect(listing.entries.find(entry => entry.name === '.env.production')).toEqual(expect.objectContaining({
      previewKind: 'text',
      syntaxLanguage: 'dotenv',
    }))
  })

  test('lists dedicated thumbnail URLs for supported raster formats only', async () => {
    const { server, root } = await startFixtureServer()
    await Promise.all([
      ...['avif', 'gif', 'jpeg', 'jpg', 'png', 'webp'].map(extension => writeFile(path.join(root, `photo.${extension}`), 'format fixture')),
      writeFile(path.join(root, 'vector.svg'), '<svg xmlns="http://www.w3.org/2000/svg"/>'),
    ])

    const response = await fetch(new URL('/api/directory?path=', server.url))
    const listing = await response.json() as { entries: Array<{ name: string, fileUrl?: string, thumbnailUrl?: string }> }

    for (const extension of ['avif', 'gif', 'jpeg', 'jpg', 'png', 'webp']) {
      expect(listing.entries.find(entry => entry.name === `photo.${extension}`)).toEqual(expect.objectContaining({
        fileUrl: `/files/photo.${extension}`,
        thumbnailUrl: `/thumbnails/photo.${extension}`,
      }))
    }
    expect(listing.entries.find(entry => entry.name === 'vector.svg')?.thumbnailUrl).toBeUndefined()
  })

  test('serves raster thumbnails without routing SVG through the converter', async () => {
    const { server, root } = await startFixtureServer()
    await Promise.all([
      writeFile(path.join(root, 'photo.png'), Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')),
      writeFile(path.join(root, 'vector.svg'), '<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"/>'),
    ])

    const thumbnail = await fetch(new URL('/thumbnails/photo.png', server.url))
    const head = await fetch(new URL('/thumbnails/photo.png', server.url), { method: 'HEAD' })
    const svg = await fetch(new URL('/thumbnails/vector.svg', server.url))

    expect(thumbnail.status).toBe(200)
    expect(thumbnail.headers.get('content-type')).toBe('image/webp')
    expect(Buffer.from(await thumbnail.arrayBuffer()).subarray(0, 4).toString('ascii')).toBe('RIFF')
    expect(head.status).toBe(200)
    expect(head.headers.get('content-length')).toBe(thumbnail.headers.get('content-length'))
    expect(await head.text()).toBe('')
    expect(svg.status).toBe(404)
  })

  test('deduplicates thumbnail requests and invalidates cached output when the file changes', async () => {
    const { server, root } = await startFixtureServer()
    const firstPng = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
    await writeFile(path.join(root, 'photo.png'), firstPng)

    const responses = await Promise.all(Array.from({ length: 4 }, () => fetch(new URL('/thumbnails/photo.png', server.url))))
    const initialEtag = responses[0]!.headers.get('etag')!
    const cached = await fetch(new URL('/thumbnails/photo.png', server.url), { headers: { 'If-None-Match': initialEtag } })
    await Bun.sleep(10)
    await writeFile(path.join(root, 'photo.png'), Buffer.concat([firstPng, Buffer.from([0])]))
    const changed = await fetch(new URL('/thumbnails/photo.png', server.url))

    expect(responses.every(response => response.status === 200)).toBe(true)
    expect(new Set(responses.map(response => response.headers.get('etag')))).toEqual(new Set([initialEtag]))
    expect(cached.status).toBe(304)
    expect(changed.status).toBe(200)
    expect(changed.headers.get('etag')).not.toBe(initialEtag)
  })

  test('rejects unsafe or unsupported thumbnail inputs before conversion', async () => {
    const { server, root } = await startFixtureServer()
    const oversizedPixels = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
    oversizedPixels.writeUInt32BE(10_000, 16)
    oversizedPixels.writeUInt32BE(5_001, 20)
    await Promise.all([
      writeFile(path.join(root, 'broken.jpg'), 'not an image'),
      writeFile(path.join(root, 'notes.txt'), 'plain text'),
      writeFile(path.join(root, 'large.jpg'), ''),
      writeFile(path.join(root, 'too-many-pixels.png'), oversizedPixels),
    ])
    await truncate(path.join(root, 'large.jpg'), THUMBNAIL_MAX_INPUT_BYTES + 1)

    const broken = await fetch(new URL('/thumbnails/broken.jpg', server.url))
    const text = await fetch(new URL('/thumbnails/notes.txt', server.url))
    const large = await fetch(new URL('/thumbnails/large.jpg', server.url))
    const tooManyPixels = await fetch(new URL('/thumbnails/too-many-pixels.png', server.url))
    const escaping = await fetch(new URL('/thumbnails/%2e%2e%2foutside.jpg', server.url))

    expect(broken.status).toBe(422)
    expect(text.status).toBe(404)
    expect(large.status).toBe(413)
    expect(tooManyPixels.status).toBe(413)
    expect(escaping.status).toBe(403)
  })

  test('times out a stalled thumbnail worker and recovers with its replacement', async () => {
    let workerNumber = 0
    const { server, root } = await startFixtureServer(false, workspace => new ThumbnailService(workspace, {
      workerCount: 1,
      timeoutMs: 10,
      createWorker: () => {
        const stalls = workerNumber++ === 0
        return fakeThumbnailWorker((worker, request) => {
          if (!stalls) {
            queueMicrotask(() => worker.onmessage?.({
              data: { id: request.id, ok: true, bytes: new Uint8Array([82, 73, 70, 70]).buffer },
            } as MessageEvent))
          }
        })
      },
    }))
    await writeFile(path.join(root, 'photo.png'), onePixelPng())

    const timedOut = await fetch(new URL('/thumbnails/photo.png', server.url))
    const recovered = await fetch(new URL('/thumbnails/photo.png', server.url))

    expect(timedOut.status).toBe(504)
    expect(recovered.status).toBe(200)
    expect(workerNumber).toBe(2)
  })

  test('rejects thumbnail work beyond the bounded queue', async () => {
    const { server, root } = await startFixtureServer(false, workspace => new ThumbnailService(workspace, {
      workerCount: 1,
      maxQueued: 2,
      timeoutMs: 10,
      createWorker: () => fakeThumbnailWorker(() => {}),
    }))
    await Promise.all(Array.from({ length: 4 }, (_, index) => writeFile(path.join(root, `photo-${index}.png`), onePixelPng())))

    const responses = await Promise.all(Array.from(
      { length: 4 },
      (_, index) => fetch(new URL(`/thumbnails/photo-${index}.png`, server.url)),
    ))

    expect(responses.map(response => response.status).sort()).toEqual([503, 504, 504, 504])
  })

  test('serves original files under /files with HEAD, download, cache, and CORS semantics', async () => {
    const { server, root } = await startFixtureServer()
    await Promise.all([
      writeFile(path.join(root, 'page.html'), '<script>alert(1)</script>'),
      writeFile(path.join(root, 'page.xhtml'), '<html xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></html>'),
      writeFile(path.join(root, 'vector.svg'), '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>'),
    ])

    const file = await fetch(new URL('/files/hello.txt', server.url))
    const head = await fetch(new URL('/files/hello.txt', server.url), { method: 'HEAD' })
    const download = await fetch(new URL('/files/hello.txt?download=1', server.url))
    const options = await fetch(new URL('/files/hello.txt', server.url), { method: 'OPTIONS' })
    const directory = await fetch(new URL('/files/', server.url), { redirect: 'manual' })
    const html = await fetch(new URL('/files/page.html', server.url))
    const xhtml = await fetch(new URL('/files/page.xhtml', server.url))
    const svg = await fetch(new URL('/files/vector.svg', server.url))

    expect(file.status).toBe(200)
    expect(await file.text()).toBe('hello world')
    expect(file.headers.get('content-type')).toBe('text/plain;charset=utf-8')
    expect(file.headers.get('content-disposition')).toStartWith('inline;')
    expect(file.headers.get('content-length')).toBe('11')
    expect(file.headers.get('last-modified')).not.toBeNull()
    expect(file.headers.get('etag')).not.toBeNull()
    expect(file.headers.get('access-control-allow-origin')).toBe('*')
    expect(file.headers.get('accept-ranges')).toBe('bytes')
    expect(head.status).toBe(200)
    expect(head.headers.get('content-length')).toBe('11')
    expect(await head.text()).toBe('')
    expect(download.headers.get('content-disposition')).toStartWith('attachment;')
    expect(options.status).toBe(204)
    expect(options.headers.get('access-control-allow-origin')).toBe('*')
    expect(options.headers.get('access-control-allow-methods')).toBe('GET, HEAD, OPTIONS')
    expect(directory.status).toBe(302)
    expect(directory.headers.get('location')).toBe('/')
    expect(html.headers.get('content-disposition')).toStartWith('inline;')
    expect(html.headers.get('content-security-policy')).toBeNull()
    expect(xhtml.headers.get('content-disposition')).toStartWith('inline;')
    expect(xhtml.headers.get('content-security-policy')).toBeNull()
    expect(svg.headers.get('content-security-policy')).toContain('sandbox')
  })

  test('forces HTML downloads when safe mode is enabled', async () => {
    const { server, root } = await startFixtureServer(false, undefined, undefined, undefined, undefined, true)
    await Promise.all([
      writeFile(path.join(root, 'page.html'), '<script>window.ready = true</script>'),
      writeFile(path.join(root, 'page.xhtml'), '<html xmlns="http://www.w3.org/1999/xhtml"><script>window.ready = true</script></html>'),
      writeFile(path.join(root, 'vector.svg'), '<svg xmlns="http://www.w3.org/2000/svg"><script>window.ready = true</script></svg>'),
      writeFile(path.join(root, 'document.xml'), '<document><script>window.ready = true</script></document>'),
    ])

    const html = await fetch(new URL('/files/page.html', server.url))
    const xhtml = await fetch(new URL('/files/page.xhtml', server.url))
    const svg = await fetch(new URL('/files/vector.svg', server.url))
    const xml = await fetch(new URL('/files/document.xml', server.url))

    expect(html.status).toBe(200)
    expect(xhtml.status).toBe(200)
    expect(await html.text()).toContain('window.ready')
    expect(await xhtml.text()).toContain('window.ready')
    expect(html.headers.get('content-disposition')).toStartWith('attachment;')
    expect(html.headers.get('content-security-policy')).toContain('sandbox')
    expect(xhtml.headers.get('content-disposition')).toStartWith('attachment;')
    expect(xhtml.headers.get('content-security-policy')).toContain('sandbox')
    expect(svg.headers.get('content-security-policy')).toContain('sandbox')
    expect(xml.headers.get('content-security-policy')).toContain('sandbox')
  })

  test('supports conditional requests and one byte range', async () => {
    const { server } = await startFixtureServer()
    const initial = await fetch(new URL('/files/hello.txt', server.url))
    const etag = initial.headers.get('etag')!

    const cached = await fetch(new URL('/files/hello.txt', server.url), {
      headers: { 'If-None-Match': etag },
    })
    const range = await fetch(new URL('/files/hello.txt', server.url), {
      headers: { Range: 'bytes=0-4' },
    })
    const suffix = await fetch(new URL('/files/hello.txt', server.url), {
      headers: { Range: 'bytes=-5' },
    })
    const invalid = await fetch(new URL('/files/hello.txt', server.url), {
      headers: { Range: 'bytes=99-120' },
    })
    const multiple = await fetch(new URL('/files/hello.txt', server.url), {
      headers: { Range: 'bytes=0-1,4-5' },
    })
    const weakIfRange = await fetch(new URL('/files/hello.txt', server.url), {
      headers: { 'Range': 'bytes=0-4', 'If-Range': etag },
    })

    expect(cached.status).toBe(304)
    expect(await cached.text()).toBe('')
    expect(range.status).toBe(206)
    expect(range.headers.get('content-range')).toBe('bytes 0-4/11')
    expect(range.headers.get('content-length')).toBe('5')
    expect(await range.text()).toBe('hello')
    expect(suffix.status).toBe(206)
    expect(await suffix.text()).toBe('world')
    expect(invalid.status).toBe(416)
    expect(invalid.headers.get('content-range')).toBe('bytes */11')
    expect(multiple.status).toBe(416)
    expect(weakIfRange.status).toBe(200)
    expect(await weakIfRange.text()).toBe('hello world')
  })

  test('serves bounded text preview results as data', async () => {
    const { server, root } = await startFixtureServer()
    await Promise.all([
      writeFile(path.join(root, 'page.html'), '<script>alert(1)</script>'),
      writeFile(path.join(root, 'bad.txt'), Uint8Array.from([0xC3, 0x28])),
    ])

    const text = await fetch(new URL('/api/text?path=page.html', server.url))
    const binary = await fetch(new URL('/api/text?path=bad.txt', server.url))
    const invalidMethod = await fetch(new URL('/api/text?path=page.html', server.url), { method: 'POST' })

    expect(await text.json()).toEqual(expect.objectContaining({
      version: 1,
      status: 'ready',
      text: '<script>alert(1)</script>',
      encoding: 'utf-8',
      size: 25,
      revision: expect.stringMatching(/^[0-9a-f]{64}$/),
    }))
    expect(text.headers.get('content-type')).toContain('application/json')
    expect(await binary.json()).toEqual({ version: 1, status: 'binary', size: 2 })
    expect(invalidMethod.status).toBe(405)
  })

  test('conditionally saves text only in management mode and rejects stale revisions', async () => {
    const disabledFixture = await startFixtureServer(false)
    const fixture = await startFixtureServer(true)
    const endpoint = new URL('/api/text?path=hello.txt', fixture.server.url)
    const preview = await fetch(endpoint)
    const previewBody = await preview.json() as { revision: string }
    const origin = fixture.server.url.origin

    const disabled = await fetch(new URL('/api/text?path=hello.txt', disabledFixture.server.url), {
      method: 'PUT',
      headers: { 'Content-Type': 'text/plain', 'Origin': disabledFixture.server.url.origin, 'If-Match': 'x' },
      body: 'blocked',
    })
    const crossOrigin = await fetch(endpoint, {
      method: 'PUT',
      headers: { 'Content-Type': 'text/plain', 'Origin': 'https://attacker.example', 'If-Match': previewBody.revision },
      body: 'blocked',
    })
    const missing = await fetch(endpoint, { method: 'PUT', headers: { 'Content-Type': 'text/plain', 'Origin': origin }, body: 'missing' })
    const wrongType = await fetch(endpoint, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'Origin': origin, 'If-Match': previewBody.revision }, body: 'wrong' })
    const saved = await fetch(endpoint, { method: 'PUT', headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Origin': origin, 'If-Match': previewBody.revision }, body: 'updated\n' })
    const stale = await fetch(endpoint, { method: 'PUT', headers: { 'Content-Type': 'text/plain', 'Origin': origin, 'If-Match': previewBody.revision }, body: 'must not overwrite' })

    expect(disabled.status).toBe(403)
    expect(crossOrigin.status).toBe(403)
    expect(missing.status).toBe(428)
    expect(wrongType.status).toBe(415)
    expect(saved.status).toBe(200)
    const savedBody = await saved.json() as { version: number, encoding: string, size: number, revision: string }
    expect(savedBody).toEqual(expect.objectContaining({ version: 1, encoding: 'utf-8', size: 7, revision: expect.stringMatching(/^[0-9a-f]{64}$/) }))
    const oversized = await fetch(endpoint, {
      method: 'PUT',
      headers: { 'Content-Type': 'text/plain', 'Origin': origin, 'If-Match': savedBody.revision },
      body: 'x'.repeat(MAX_TEXT_PREVIEW_BYTES + 1),
    })

    expect(oversized.status).toBe(413)
    expect(stale.status).toBe(412)
    expect(await (await fetch(new URL('/files/hello.txt', fixture.server.url))).text()).toBe('updated')
  }, { timeout: 20_000 })

  test('uploads only when enabled and requested by the bound same origin', async () => {
    const disabledFixture = await startFixtureServer(false)
    const enabledFixture = await startFixtureServer(true)
    const form = (): FormData => {
      const body = new FormData()
      body.set('file', new File(['uploaded'], 'notes.txt'))
      return body
    }

    const disabled = await fetch(new URL('/api/upload?path=', disabledFixture.server.url), {
      method: 'POST',
      headers: { Origin: disabledFixture.server.url.origin },
      body: form(),
    })
    const missingOrigin = await fetch(new URL('/api/upload?path=', enabledFixture.server.url), {
      method: 'POST',
      body: form(),
    })
    const crossOrigin = await fetch(new URL('/api/upload?path=', enabledFixture.server.url), {
      method: 'POST',
      headers: { Origin: 'https://attacker.example' },
      body: form(),
    })
    const unsupportedMediaType = await fetch(new URL('/api/upload?path=', enabledFixture.server.url), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Origin': enabledFixture.server.url.origin,
      },
      body: '{}',
    })
    const accepted = await fetch(new URL('/api/upload?path=', enabledFixture.server.url), {
      method: 'POST',
      headers: { Origin: enabledFixture.server.url.origin },
      body: form(),
    })

    expect(disabled.status).toBe(403)
    expect(await disabled.json()).toEqual(expect.objectContaining({ error: expect.objectContaining({ code: 'MANAGEMENT_DISABLED' }) }))
    expect(missingOrigin.status).toBe(403)
    expect(crossOrigin.status).toBe(403)
    expect(unsupportedMediaType.status).toBe(415)
    expect(await unsupportedMediaType.json()).toEqual(expect.objectContaining({
      error: expect.objectContaining({ code: 'UNSUPPORTED_MEDIA_TYPE' }),
    }))
    expect(await accepted.json()).toEqual({
      version: 1,
      filename: 'notes.txt',
      path: 'notes.txt',
      size: 8,
    })
    const listing = await fetch(new URL('/api/directory?path=', enabledFixture.server.url)).then(response => response.json())
    expect(listing.entries).toEqual(expect.arrayContaining([expect.objectContaining({ name: 'notes.txt' })]))
  })

  test('uploads large files through ordered chunk sessions and publishes atomically', async () => {
    const { server, root } = await startFixtureServer(true, undefined, undefined, undefined, undefined, false, true)
    const origin = server.url.origin
    const total = 20 * 1024 * 1024 + 2
    const capability = await fetch(new URL('/api/directory', server.url)).then(response => response.json()) as { chunkedUpload?: { thresholdBytes: number, chunkSizeBytes: number } }
    expect(capability.chunkedUpload).toEqual({ thresholdBytes: 20 * 1024 * 1024, chunkSizeBytes: 8 * 1024 * 1024 })
    const created = await fetch(new URL('/api/uploads', server.url), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Origin': origin },
      body: JSON.stringify({ directoryPath: '', filename: 'large.bin', size: total }),
    })
    const createdBody = await created.json() as { upload: { id: string, chunkSizeBytes: number, uploadedBytes: number } }
    expect(created.status).toBe(201)
    expect(createdBody.upload).toEqual(expect.objectContaining({ chunkSizeBytes: 8 * 1024 * 1024, uploadedBytes: 0 }))

    const first = new Uint8Array(8 * 1024 * 1024).fill(0x41)
    const second = new Uint8Array(8 * 1024 * 1024).fill(0x42)
    const third = new Uint8Array(total - first.byteLength - second.byteLength).fill(0x43)
    const wrongOffset = await fetch(new URL(`/api/uploads/${createdBody.upload.id}`, server.url), {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Range': `bytes 1-1/${total}`,
        'Origin': origin,
      },
      body: new Uint8Array([0]),
    })
    expect(wrongOffset.status).toBe(409)
    const firstResponse = await fetch(new URL(`/api/uploads/${createdBody.upload.id}`, server.url), {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Range': `bytes 0-${first.byteLength - 1}/${total}`,
        'Origin': origin,
      },
      body: first,
    })
    expect(firstResponse.status).toBe(200)
    expect((await firstResponse.json()).upload.uploadedBytes).toBe(first.byteLength)

    const incomplete = await fetch(new URL(`/api/uploads/${createdBody.upload.id}/complete`, server.url), {
      method: 'POST',
      headers: { Origin: origin },
    })
    expect(incomplete.status).toBe(409)

    const secondResponse = await fetch(new URL(`/api/uploads/${createdBody.upload.id}`, server.url), {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Range': `bytes ${first.byteLength}-${first.byteLength + second.byteLength - 1}/${total}`,
        'Origin': origin,
      },
      body: second,
    })
    expect(secondResponse.status).toBe(200)

    const thirdResponse = await fetch(new URL(`/api/uploads/${createdBody.upload.id}`, server.url), {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/octet-stream',
        'Content-Range': `bytes ${first.byteLength + second.byteLength}-${total - 1}/${total}`,
        'Origin': origin,
      },
      body: third,
    })
    expect(thirdResponse.status).toBe(200)

    const completed = await fetch(new URL(`/api/uploads/${createdBody.upload.id}/complete`, server.url), {
      method: 'POST',
      headers: { Origin: origin },
    })
    const completedBody = await completed.json() as { upload: { status: string, result: { filename: string, size: number } } }
    expect(completed.status).toBe(200)
    expect(completedBody.upload).toEqual(expect.objectContaining({ status: 'complete', result: expect.objectContaining({ filename: 'large.bin', size: total }) }))
    expect((await fetch(new URL(`/files/${completedBody.upload.result.filename}`, server.url), { method: 'HEAD' })).headers.get('content-length')).toBe(String(total))
    const listing = await fetch(new URL('/api/directory', server.url))
    expect(await listing.json()).toEqual(expect.objectContaining({
      entries: expect.not.arrayContaining([expect.objectContaining({ name: expect.stringMatching(/^\.upload-/) })]),
    }))
    expect((await readdir(root)).some(name => name.startsWith('.upload-'))).toBe(false)
  })

  test('bounds chunked-upload session request bodies before JSON parsing', async () => {
    const { server } = await startFixtureServer(true, undefined, undefined, undefined, undefined, false, true)
    const body = JSON.stringify({ directoryPath: '', filename: 'large.bin', size: 20 * 1024 * 1024 + 1 }) + ' '.repeat(64 * 1024)
    const response = await fetch(new URL('/api/uploads', server.url), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Origin': server.url.origin },
      body,
    })

    expect(response.status).toBe(413)
    expect(await response.json()).toEqual(expect.objectContaining({ error: expect.objectContaining({ code: 'TOO_LARGE' }) }))
    expect((await fetch(new URL('/api/directory', server.url))).status).toBe(200)
  })

  test('applies validated filesystem operations only in same-origin management mode', async () => {
    const disabledFixture = await startFixtureServer(false)
    const enabledFixture = await startFixtureServer(true)
    const request = (server: RunningFsServer, body: unknown, origin = server.url.origin): Promise<Response> => fetch(new URL('/api/operations', server.url), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Origin': origin },
      body: JSON.stringify(body),
    })

    const disabled = await request(disabledFixture.server, { action: 'delete', paths: ['hello.txt'] })
    const crossOrigin = await request(enabledFixture.server, { action: 'delete', paths: ['hello.txt'] }, 'https://attacker.example')
    const invalid = await request(enabledFixture.server, { action: 'copy', paths: [], destinationPath: '' })
    const unsupportedAction = await request(enabledFixture.server, { action: 'archive', paths: ['hello.txt'] })
    const created = await request(enabledFixture.server, { action: 'create-directory', parentPath: '', name: 'projects' })
    const deleted = await request(enabledFixture.server, { action: 'delete', paths: ['hello.txt', 'missing.txt'] })

    expect(disabled.status).toBe(403)
    expect(await disabled.json()).toEqual(expect.objectContaining({ error: expect.objectContaining({ code: 'MANAGEMENT_DISABLED' }) }))
    expect(crossOrigin.status).toBe(403)
    expect(invalid.status).toBe(400)
    expect(await invalid.json()).toEqual(expect.objectContaining({ error: expect.objectContaining({ code: 'INVALID_OPERATION' }) }))
    expect(unsupportedAction.status).toBe(400)
    expect(await unsupportedAction.json()).toEqual(expect.objectContaining({ error: expect.objectContaining({ code: 'INVALID_OPERATION' }) }))
    expect(await created.json()).toEqual({
      version: 1,
      action: 'create-directory',
      items: [{ status: 'ok', destinationPath: 'projects' }],
    })
    expect(await deleted.json()).toEqual({
      version: 1,
      action: 'delete',
      items: [
        { status: 'ok', sourcePath: 'hello.txt' },
        { status: 'error', sourcePath: 'missing.txt', error: { code: 'NOT_FOUND', message: 'Path does not exist' } },
      ],
    })
  })

  test('creates, streams, observes, cancels, and retries remote download tasks', async () => {
    let attempts = 0
    const fetchImpl = async (): Promise<Response> => {
      attempts++
      return attempts === 1
        ? new Response('temporary failure', { status: 503 })
        : new Response(Uint8Array.from([7, 8, 9]), {
            headers: {
              'Content-Disposition': 'attachment; filename="remote.bin"',
              'Content-Length': '3',
            },
          })
    }
    const fixture = await startFixtureServer(true, undefined, workspace => createRemoteDownloadManager(workspace, {
      fetchImpl,
      idleTimeoutMs: 100,
    }))
    const origin = fixture.server.url.origin
    const request = (body: unknown, method = 'POST'): Promise<Response> => fetch(new URL('/api/downloads', fixture.server.url), {
      method,
      headers: {
        'Content-Type': 'application/json',
        'Origin': origin,
      },
      body: method === 'DELETE' ? undefined : JSON.stringify(body),
    })

    const disabled = await fetch(new URL('/api/downloads', (await startFixtureServer(false)).server.url))
    expect(disabled.status).toBe(403)

    const created = await request({ url: 'https://example.test/file', directoryPath: '' })
    expect(created.status).toBe(202)
    const failedBody = await created.json() as { task: { id: string } }
    let failed: { status: string } | undefined
    for (let index = 0; index < 100; index++) {
      const response = await fetch(new URL('/api/downloads', fixture.server.url))
      const body = await response.json() as { tasks: Array<{ id: string, status: string }> }
      failed = body.tasks.find(task => task.id === failedBody.task.id)
      if (failed?.status === 'error')
        break
      await Bun.sleep(2)
    }
    expect(failed?.status).toBe('error')

    const missingCancel = await fetch(new URL('/api/downloads/missing/cancel', fixture.server.url), {
      method: 'POST',
      headers: { Origin: origin },
    })
    expect(missingCancel.status).toBe(404)

    const events = await fetch(new URL('/api/downloads/events', fixture.server.url))
    const eventReader = events.body!.getReader()
    const event = await eventReader.read()
    await eventReader.cancel()
    expect(new TextDecoder().decode(event.value)).toContain(failedBody.task.id)

    const retried = await fetch(new URL(`/api/downloads/${failedBody.task.id}/retry`, fixture.server.url), {
      method: 'POST',
      headers: { Origin: origin },
    })
    expect(retried.status).toBe(202)
    const retriedBody = await retried.json() as { task: { id: string } }
    for (let index = 0; index < 100; index++) {
      const response = await fetch(new URL('/api/downloads', fixture.server.url))
      const body = await response.json() as { tasks: Array<{ id: string, status: string }> }
      if (body.tasks.find(task => task.id === retriedBody.task.id)?.status === 'done')
        break
      await Bun.sleep(2)
    }
    expect((await fetch(new URL('/files/remote.bin', fixture.server.url))).status).toBe(200)
    expect(await (await fetch(new URL('/files/remote.bin', fixture.server.url))).text()).toBe('\x07\x08\x09')

    const cleared = await fetch(new URL('/api/downloads?terminal=1', fixture.server.url), {
      method: 'DELETE',
      headers: { Origin: origin },
    })
    expect(cleared.status).toBe(204)
    expect((await fetch(new URL('/api/downloads', fixture.server.url)).then(response => response.json()) as { tasks: unknown[] }).tasks).toHaveLength(0)
  })

  test('validates, observes, retries, and clears archive extraction tasks', async () => {
    let attempts = 0
    const fixture = await startFixtureServer(true, undefined, undefined, undefined, () => createExtractionManager({
      async extractArchive(archivePath, options = {}) {
        attempts++
        options.onInspect?.({ uncompressedBytes: 12, entryCount: 2 })
        if (attempts === 1)
          throw new Error('damaged archive')
        options.onProgress?.(100)
        return {
          archivePath,
          destinationPath: 'backup',
          uncompressedBytes: 12,
          entryCount: 2,
        }
      },
    }))
    const origin = fixture.server.url.origin
    const endpoint = new URL('/api/extractions', fixture.server.url)
    const post = (body: unknown, headers: HeadersInit = { 'Content-Type': 'application/json', 'Origin': origin }): Promise<Response> => fetch(endpoint, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
    })

    const disabled = await fetch(new URL('/api/extractions', (await startFixtureServer(false)).server.url))
    expect(disabled.status).toBe(403)
    expect((await fetch(endpoint, { method: 'PUT' })).status).toBe(405)
    expect((await post({ paths: ['backup.zip'] }, { 'Content-Type': 'application/json' })).status).toBe(403)
    expect((await post({ paths: ['backup.zip'] }, { 'Content-Type': 'text/plain', 'Origin': origin })).status).toBe(415)
    expect((await post({ paths: [] })).status).toBe(400)
    expect((await post({ paths: Array.from({ length: 101 }, (_, index) => `${index}.zip`) })).status).toBe(400)

    const created = await post({ paths: ['backup.zip'] })
    expect(created.status).toBe(202)
    const createdBody = await created.json() as { tasks: Array<{ id: string }> }
    const failedId = createdBody.tasks[0]!.id
    for (let index = 0; index < 100; index++) {
      const tasks = await fetch(endpoint).then(response => response.json()) as { tasks: Array<{ id: string, status: string }> }
      if (tasks.tasks.find(task => task.id === failedId)?.status === 'error')
        break
      await Bun.sleep(2)
    }

    const events = await fetch(new URL('/api/extractions/events', fixture.server.url))
    const eventReader = events.body!.getReader()
    const event = await eventReader.read()
    await eventReader.cancel()
    expect(events.headers.get('content-type')).toContain('text/event-stream')
    expect(new TextDecoder().decode(event.value)).toContain(failedId)

    const missingCancel = await fetch(new URL('/api/extractions/missing/cancel', fixture.server.url), {
      method: 'POST',
      headers: { Origin: origin },
    })
    expect(missingCancel.status).toBe(404)
    const retried = await fetch(new URL(`/api/extractions/${failedId}/retry`, fixture.server.url), {
      method: 'POST',
      headers: { Origin: origin },
    })
    expect(retried.status).toBe(202)
    const retriedBody = await retried.json() as { task: { id: string } }
    for (let index = 0; index < 100; index++) {
      const tasks = await fetch(endpoint).then(response => response.json()) as { tasks: Array<{ id: string, status: string }> }
      if (tasks.tasks.find(task => task.id === retriedBody.task.id)?.status === 'done')
        break
      await Bun.sleep(2)
    }

    const cleared = await fetch(new URL('/api/extractions?terminal=1', fixture.server.url), {
      method: 'DELETE',
      headers: { Origin: origin },
    })
    expect(cleared.status).toBe(204)
    expect((await fetch(endpoint).then(response => response.json()) as { tasks: unknown[] }).tasks).toHaveLength(0)
  })

  test('serves the embedded React shell for root and browser routes only', async () => {
    const { server } = await startFixtureServer()

    const root = await fetch(server.url)
    const emptyBrowser = await fetch(new URL('/browse', server.url))
    const nested = await fetch(new URL('/browse/docs/examples', server.url))
    const invalidMethod = await fetch(server.url, { method: 'POST' })
    const missingApi = await fetch(new URL('/api/unknown', server.url))

    expect(root.status).toBe(200)
    const html = await root.text()
    expect(html).toContain('<div id="root"></div>')
    expect(html).toContain('<title>HACKYCY CLI - FILE BROWSER</title>')
    expect(html).toContain('http-equiv="Content-Security-Policy"')
    expect(html).toContain('default-src \'self\'')
    expect(html).toContain('worker-src \'self\'')
    expect(emptyBrowser.status).toBe(200)
    expect(nested.status).toBe(200)
    expect(nested.headers.get('content-type')).toContain('text/html')
    expect(invalidMethod.status).toBe(405)
    expect(await invalidMethod.json()).toEqual({
      version: 1,
      error: { code: 'METHOD_NOT_ALLOWED', message: 'Use GET' },
    })
    expect(missingApi.status).toBe(404)
    expect(await missingApi.json()).toEqual({
      version: 1,
      error: { code: 'NOT_FOUND', message: 'Route not found' },
    })
  })
})
