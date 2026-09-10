import type { RunningDiffServer } from './server'
import type { ComparisonSnapshot, ComparisonWorkspace, WorkspaceState } from './types'
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { startDiffHttpServer } from './server'
import { createComparisonWorkspace } from './workspace'

const temporaryDirectories: string[] = []
const servers: RunningDiffServer[] = []

function createCancelableWorkspace(): ComparisonWorkspace {
  let state: WorkspaceState = { phase: 'idle' }
  const listeners = new Set<(nextState: WorkspaceState) => void>()
  const publish = (nextState: WorkspaceState): void => {
    state = nextState
    for (const listener of listeners)
      listener(state)
  }

  return {
    state: () => state,
    refresh() {
      let cancel = (): void => {}
      const result = new Promise<ComparisonSnapshot>(() => {
        cancel = () => {
          publish({ phase: 'canceled' })
        }
      })
      publish({ phase: 'discovering' })
      return { result, cancel }
    },
    snapshot: () => undefined,
    observe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

afterEach(async () => {
  await Promise.all(servers.splice(0).map(server => server.stop()))
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function startFixtureServer(): Promise<{ server: RunningDiffServer, snapshotId: string, target: string }> {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-diff-http-'))
  temporaryDirectories.push(root)
  const baseline = path.join(root, 'baseline')
  const target = path.join(root, 'target')
  await Promise.all([mkdir(baseline), mkdir(target)])
  await Promise.all([
    writeFile(path.join(baseline, 'changed.txt'), 'before'),
    writeFile(path.join(target, 'changed.txt'), 'after'),
  ])
  const workspace = await createComparisonWorkspace({
    baselineDirectory: baseline,
    targetDirectory: target,
  })
  const snapshot = await workspace.refresh().result
  const server = startDiffHttpServer({ workspace, address: '127.0.0.1', port: 0 })
  servers.push(server)
  return { server, snapshotId: snapshot.summary.id, target }
}

describe('DiffHttpServer', () => {
  test('serves workspace state and snapshot-bound entry pages with security headers', async () => {
    const { server, snapshotId } = await startFixtureServer()
    const stateResponse = await fetch(new URL('/api/state', server.url))
    const entriesResponse = await fetch(new URL(`/api/entries?snapshot=${snapshotId}`, server.url))
    const searchResponse = await fetch(new URL(`/api/search?snapshot=${snapshotId}&q=CHANGED&status=modified`, server.url))
    const invalidSearchResponse = await fetch(new URL(`/api/search?snapshot=${snapshotId}&q=changed&limit=201`, server.url))
    const anchoredResponse = await fetch(new URL(`/api/entries?snapshot=${snapshotId}&anchor=1`, server.url))
    const invalidAnchorResponse = await fetch(new URL(`/api/entries?snapshot=${snapshotId}&anchor=nope`, server.url))
    const invalidMethodResponse = await fetch(new URL('/api/state', server.url), { method: 'POST' })

    expect(await stateResponse.json()).toEqual({
      version: 1,
      workspace: { phase: 'ready', snapshotId },
      snapshot: expect.objectContaining({
        id: snapshotId,
        counts: { added: 0, deleted: 0, modified: 1, unchanged: 0 },
      }),
    })
    expect(entriesResponse.status).toBe(200)
    expect(await entriesResponse.json()).toEqual({
      entries: [expect.objectContaining({ path: 'changed.txt', status: 'modified' })],
    })
    expect(entriesResponse.headers.get('cache-control')).toBe('no-store')
    expect(entriesResponse.headers.get('x-content-type-options')).toBe('nosniff')
    expect(entriesResponse.headers.get('referrer-policy')).toBe('no-referrer')
    expect(await searchResponse.json()).toEqual({
      results: [expect.objectContaining({ path: 'changed.txt', status: 'modified' })],
      truncated: false,
    })
    expect(invalidSearchResponse.status).toBe(400)
    expect(await anchoredResponse.json()).toEqual({
      entries: [expect.objectContaining({ path: 'changed.txt', status: 'modified' })],
    })
    expect(invalidAnchorResponse.status).toBe(400)
    expect(invalidMethodResponse.status).toBe(405)
  })

  test('serves tree, detail, text, and original bytes only for the requested snapshot', async () => {
    const { server, snapshotId, target } = await startFixtureServer()
    const treeResponse = await fetch(new URL(`/api/tree?snapshot=${snapshotId}&path=`, server.url))
    const detailResponse = await fetch(new URL(`/api/entries/1?snapshot=${snapshotId}`, server.url))
    const contentResponse = await fetch(new URL(`/api/entries/1/content/target?snapshot=${snapshotId}`, server.url))
    const blobResponse = await fetch(new URL(`/api/entries/1/blob/target?snapshot=${snapshotId}`, server.url))
    const conflictResponse = await fetch(new URL('/api/entries?snapshot=old-snapshot', server.url))

    expect(await treeResponse.json()).toEqual({
      children: [expect.objectContaining({ path: 'changed.txt', status: 'modified' })],
    })
    expect(await detailResponse.json()).toEqual(expect.objectContaining({
      id: 1,
      path: 'changed.txt',
      presentation: 'text',
    }))
    expect(await contentResponse.json()).toEqual(expect.objectContaining({
      status: 'ready',
      text: 'after',
    }))
    expect(blobResponse.headers.get('content-type')).toBe('application/octet-stream')
    expect(blobResponse.headers.get('content-disposition')).toContain('attachment')
    expect(await blobResponse.text()).toBe('after')
    expect(conflictResponse.status).toBe(409)
    expect(await conflictResponse.json()).toEqual({
      version: 1,
      error: {
        code: 'SNAPSHOT_CHANGED',
        message: 'The requested Comparison Snapshot is no longer available',
      },
    })

    const outside = path.join(path.dirname(target), 'outside-secret.txt')
    await writeFile(outside, 'secret bytes that must stay outside the snapshot')
    await rm(path.join(target, 'changed.txt'))
    await symlink(outside, path.join(target, 'changed.txt'))
    const staleContent = await fetch(new URL(`/api/entries/1/content/target?snapshot=${snapshotId}`, server.url))
    const staleBlob = await fetch(new URL(`/api/entries/1/blob/target?snapshot=${snapshotId}`, server.url))
    expect(await staleContent.json()).toEqual({ status: 'stale' })
    expect(staleBlob.status).toBe(409)
    expect(await staleBlob.text()).not.toContain('secret bytes')
  })

  test('serves only allowlisted images inline and sandboxes SVG responses', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'ycy-diff-http-images-'))
    temporaryDirectories.push(root)
    const baseline = path.join(root, 'baseline')
    const target = path.join(root, 'target')
    await Promise.all([mkdir(baseline), mkdir(target)])
    await Promise.all([
      writeFile(path.join(target, 'graphic.svg'), '<svg><script>alert(1)</script></svg>'),
      writeFile(path.join(target, 'unknown.bin'), Uint8Array.from([0, 1, 2])),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const server = startDiffHttpServer({ workspace, address: '127.0.0.1', port: 0 })
    servers.push(server)
    const entries = snapshot.list({ includeUnchanged: true }).entries
    const graphic = entries.find(entry => entry.path === 'graphic.svg')!
    const unknown = entries.find(entry => entry.path === 'unknown.bin')!

    const imageResponse = await fetch(new URL(`/api/entries/${graphic.id}/blob/target?snapshot=${snapshot.summary.id}`, server.url))
    const binaryResponse = await fetch(new URL(`/api/entries/${unknown.id}/blob/target?snapshot=${snapshot.summary.id}`, server.url))
    expect(imageResponse.headers.get('content-type')).toBe('image/svg+xml')
    expect(imageResponse.headers.get('content-disposition')).toStartWith('inline;')
    expect(imageResponse.headers.get('content-security-policy')).toContain('sandbox; default-src \'none\'')
    expect(binaryResponse.headers.get('content-type')).toBe('application/octet-stream')
    expect(binaryResponse.headers.get('content-disposition')).toStartWith('attachment;')
  })

  test('streams state and accepts refresh mutations only from the same origin', async () => {
    const { server, snapshotId, target } = await startFixtureServer()
    const eventsResponse = await fetch(new URL('/api/events', server.url))
    const reader = eventsResponse.body!.getReader()
    const firstEvent = await reader.read()
    await reader.cancel()

    expect(eventsResponse.headers.get('content-type')).toContain('text/event-stream')
    expect(new TextDecoder().decode(firstEvent.value)).toContain(`"snapshotId":"${snapshotId}"`)

    const rejected = await fetch(new URL('/api/refresh', server.url), {
      method: 'POST',
      headers: { Origin: 'https://attacker.example' },
    })
    expect(rejected.status).toBe(403)

    await writeFile(path.join(target, 'added.txt'), 'new')
    const accepted = await fetch(new URL('/api/refresh', server.url), {
      method: 'POST',
      headers: { Origin: server.url.origin },
    })
    expect(accepted.status).toBe(202)

    let newSnapshotId = snapshotId
    for (let attempt = 0; attempt < 50 && newSnapshotId === snapshotId; attempt++) {
      const state = await fetch(new URL('/api/state', server.url)).then(response => response.json())
      newSnapshotId = state.snapshot?.id ?? snapshotId
      if (newSnapshotId === snapshotId)
        await Bun.sleep(5)
    }
    expect(newSnapshotId).not.toBe(snapshotId)
    const oldSnapshot = await fetch(new URL(`/api/entries?snapshot=${snapshotId}`, server.url))
    expect(oldSnapshot.status).toBe(409)
  })

  test('tracks and cancels the initial refresh started by the HTTP server', async () => {
    const workspace = createCancelableWorkspace()
    const server = startDiffHttpServer({ workspace, address: '127.0.0.1', port: 0, initialRefresh: true })
    servers.push(server)

    for (let attempt = 0; attempt < 100 && workspace.state().phase === 'idle'; attempt++)
      await Bun.sleep(1)
    const shellResponse = await fetch(server.url)
    expect(shellResponse.status).toBe(200)
    expect(workspace.state().phase).not.toBe('ready')
    const response = await fetch(new URL('/api/refresh', server.url), {
      method: 'DELETE',
      headers: { Origin: server.url.origin },
    })
    expect(response.status).toBe(204)
    for (let attempt = 0; attempt < 100 && workspace.state().phase !== 'canceled'; attempt++)
      await Bun.sleep(1)
    expect(workspace.state().phase).toBe('canceled')
    expect(workspace.snapshot()).toBeUndefined()
  })

  test('resolves the server lifecycle promise after graceful shutdown', async () => {
    const { server } = await startFixtureServer()
    const finished = server.finished
    await server.stop()
    await expect(finished).resolves.toBeUndefined()
    servers.splice(servers.indexOf(server), 1)
  })
})
