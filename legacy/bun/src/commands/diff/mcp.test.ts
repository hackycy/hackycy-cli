import type { RunningDiffServer } from './server'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js'
import { afterEach, expect, test } from 'bun:test'
import { startDiffHttpServer } from './server'
import { createComparisonWorkspace } from './workspace'

const temporaryDirectories: string[] = []
const servers: RunningDiffServer[] = []
const clients: Client[] = []

afterEach(async () => {
  await Promise.all(clients.splice(0).map(client => client.close()))
  await Promise.all(servers.splice(0).map(server => server.stop()))
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function startFixture(): Promise<{ client: Client, baseline: string, target: string, url: URL }> {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-diff-mcp-'))
  temporaryDirectories.push(root)
  const baseline = path.join(root, 'baseline')
  const target = path.join(root, 'target')
  await Promise.all([mkdir(baseline), mkdir(target)])
  await Promise.all([
    writeFile(path.join(target, 'added.txt'), 'added\n'),
    writeFile(path.join(baseline, 'changed.txt'), 'before\n'),
    writeFile(path.join(target, 'changed.txt'), 'after\n'),
    writeFile(path.join(baseline, 'deleted.txt'), 'deleted\n'),
  ])
  if (process.platform !== 'win32') {
    const mkfifo = Bun.spawn(['mkfifo', path.join(target, 'service.pipe')], { stdout: 'ignore', stderr: 'pipe' })
    expect(await mkfifo.exited).toBe(0)
  }
  const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
  const snapshot = await workspace.refresh().result
  const server = startDiffHttpServer({ workspace, address: '127.0.0.1', port: 0 })
  servers.push(server)
  const client = new Client({ name: 'diff-mcp-test', version: '1.0.0' })
  clients.push(client)
  await client.connect(new StreamableHTTPClientTransport(new URL('/mcp', server.url)))
  return {
    client,
    baseline: snapshot.summary.baselineDirectory,
    target: snapshot.summary.targetDirectory,
    url: server.url,
  }
}

function expectSecurityHeaders(response: Response): void {
  expect(response.headers.get('cache-control')).toBe('no-store')
  expect(response.headers.get('x-content-type-options')).toBe('nosniff')
  expect(response.headers.get('referrer-policy')).toBe('no-referrer')
  expect(response.headers.get('content-security-policy')).toContain('default-src \'self\'')
}

test('publishes the fixed Directory Comparison tool interface', async () => {
  const { client } = await startFixture()

  const tools = (await client.listTools()).tools
  expect(tools.map(tool => tool.name)).toEqual([
    'get_comparison',
    'refresh_comparison',
    'list_changes',
    'list_issues',
    'search_changes',
    'get_text_diff',
  ])
  expect(tools.find(tool => tool.name === 'refresh_comparison')?.annotations?.idempotentHint).toBe(false)
})

test('returns the current Comparison Snapshot as structured knowledge', async () => {
  const { client, baseline, target } = await startFixture()

  const result = await client.callTool({ name: 'get_comparison', arguments: {} })

  expect(result.structuredContent).toEqual({
    phase: 'ready',
    snapshot: {
      snapshot_id: expect.any(String),
      baseline_directory: baseline,
      target_directory: target,
      created_at: expect.any(String),
      counts: { added: 1, deleted: 1, modified: 1, unchanged: 0 },
      issues: process.platform === 'win32' ? 0 : 1,
    },
  })
  expect(result.content).toEqual([{
    type: 'text',
    text: process.platform === 'win32'
      ? 'Comparison ready: 3 changes, 0 issues'
      : 'Comparison ready: 3 changes, 1 issue',
  }])
})

test('lists changed Entry States with opaque cursor pagination', async () => {
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id

  const first = await client.callTool({
    name: 'list_changes',
    arguments: { snapshot_id: snapshotId, limit: 2 },
  })
  const firstPage = first.structuredContent as { changes: unknown[], next_cursor: string }
  const second = await client.callTool({
    name: 'list_changes',
    arguments: { snapshot_id: snapshotId, limit: 2, cursor: firstPage.next_cursor },
  })

  expect(firstPage).toEqual({
    changes: [
      {
        entry_id: expect.any(Number),
        path: 'added.txt',
        status: 'added',
        target: { kind: 'file', size: 6 },
      },
      {
        entry_id: expect.any(Number),
        path: 'changed.txt',
        status: 'modified',
        baseline: { kind: 'file', size: 7 },
        target: { kind: 'file', size: 6 },
      },
    ],
    next_cursor: expect.any(String),
  })
  expect(second.structuredContent).toEqual({
    changes: [{
      entry_id: expect.any(Number),
      path: 'deleted.txt',
      status: 'deleted',
      baseline: { kind: 'file', size: 8 },
    }],
  })
})

test('lists Comparison Issues separately from changed entries', async () => {
  if (process.platform === 'win32')
    return
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id

  const result = await client.callTool({
    name: 'list_issues',
    arguments: { snapshot_id: snapshotId },
  })

  expect(result.structuredContent).toEqual({
    issues: [{
      path: 'service.pipe',
      message: expect.any(String),
    }],
  })
  expect(result.content).toEqual([{ type: 'text', text: 'Listed 1 Comparison Issue' }])
})

test('searches changed Comparison Paths case-insensitively with a bounded result', async () => {
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id

  const result = await client.callTool({
    name: 'search_changes',
    arguments: { snapshot_id: snapshotId, query: '.TXT', limit: 2 },
  })

  expect(result.structuredContent).toEqual({
    changes: [
      {
        entry_id: expect.any(Number),
        path: 'added.txt',
        status: 'added',
        target: { kind: 'file', size: 6 },
      },
      {
        entry_id: expect.any(Number),
        path: 'changed.txt',
        status: 'modified',
        baseline: { kind: 'file', size: 7 },
        target: { kind: 'file', size: 6 },
      },
    ],
    truncated: true,
  })
})

test('returns an on-demand Unified Diff for an opaque Entry ID', async () => {
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id
  const page = await client.callTool({
    name: 'list_changes',
    arguments: { snapshot_id: snapshotId, statuses: ['modified'] },
  })
  const changedEntry = (page.structuredContent as { changes: Array<{ entry_id: number }> }).changes[0]!

  const result = await client.callTool({
    name: 'get_text_diff',
    arguments: { snapshot_id: snapshotId, entry_id: changedEntry.entry_id, context_lines: 1 },
  })

  expect(result.structuredContent).toEqual({
    status: 'ready',
    path: 'changed.txt',
    comparison_status: 'modified',
    context_lines: 1,
    baseline_encoding: 'utf-8',
    target_encoding: 'utf-8',
    added_lines: 1,
    deleted_lines: 1,
    patch: '--- baseline\n+++ target\n@@ -1,1 +1,1 @@\n-before\n+after\n',
  })
})

test('starts one asynchronous Refresh and reports concurrent requests as already running', async () => {
  const { client, baseline, target, url } = await startFixture()
  const baselineLoad = path.join(baseline, 'refresh-load')
  const targetLoad = path.join(target, 'refresh-load')
  await Promise.all([mkdir(baselineLoad), mkdir(targetLoad)])
  await Promise.all(Array.from({ length: 300 }, async (_, index) => {
    await Promise.all([
      writeFile(path.join(baselineLoad, `${index}.txt`), 'same content\n'),
      writeFile(path.join(targetLoad, `${index}.txt`), 'same content\n'),
    ])
  }))

  const results = await Promise.all([
    client.callTool({ name: 'refresh_comparison', arguments: {} }),
    client.callTool({ name: 'refresh_comparison', arguments: {} }),
  ])

  expect(results.map(result => result.structuredContent)).toContainEqual({
    accepted: true,
    already_running: false,
  })
  expect(results.map(result => result.structuredContent)).toContainEqual({
    accepted: false,
    already_running: true,
  })

  await fetch(new URL('/api/refresh', url), {
    method: 'DELETE',
    headers: { Origin: url.origin },
  })
  let phase: unknown
  for (let attempt = 0; attempt < 50 && phase !== 'canceled'; attempt++) {
    const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
    phase = (comparison.structuredContent as { phase?: unknown } | undefined)?.phase
    if (phase !== 'canceled')
      await Bun.sleep(5)
  }
  expect(phase).toBe('canceled')
})

test('rejects cross-origin MCP requests without enabling CORS', async () => {
  const { url } = await startFixture()

  const response = await fetch(new URL('/mcp', url), {
    method: 'POST',
    headers: {
      'Accept': 'application/json, text/event-stream',
      'Content-Type': 'application/json',
      'Origin': 'https://attacker.example',
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {
        protocolVersion: '2025-06-18',
        capabilities: {},
        clientInfo: { name: 'cross-origin-test', version: '1.0.0' },
      },
    }),
  })

  expect(response.status).toBe(403)
  expectSecurityHeaders(response)
  expect(response.headers.get('access-control-allow-origin')).toBeNull()
})

test('rejects a same-origin request whose host is not allowed by the server binding', async () => {
  const { url } = await startFixture()
  const rebindingOrigin = `http://attacker.example:${url.port}`

  const response = await fetch(new URL('/mcp', url), {
    method: 'POST',
    headers: {
      'Accept': 'application/json, text/event-stream',
      'Content-Type': 'application/json',
      'Host': `attacker.example:${url.port}`,
      'Origin': rebindingOrigin,
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {
        protocolVersion: '2025-06-18',
        capabilities: {},
        clientInfo: { name: 'dns-rebinding-test', version: '1.0.0' },
      },
    }),
  })

  expect(response.status).toBe(403)
  expectSecurityHeaders(response)
})

test('applies no-store and security headers to successful MCP responses', async () => {
  const { url } = await startFixture()

  const response = await fetch(new URL('/mcp', url), {
    method: 'POST',
    headers: {
      'Accept': 'application/json, text/event-stream',
      'Content-Type': 'application/json',
      'Origin': url.origin,
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {
        protocolVersion: '2025-06-18',
        capabilities: {},
        clientInfo: { name: 'response-header-test', version: '1.0.0' },
      },
    }),
  })

  expect(response.status).toBe(200)
  expectSecurityHeaders(response)
})

test('serves independent clients without MCP session state', async () => {
  const { client, url } = await startFixture()
  const secondClient = new Client({ name: 'second-diff-mcp-test', version: '1.0.0' })
  clients.push(secondClient)
  const transport = new StreamableHTTPClientTransport(new URL('/mcp', url))
  await secondClient.connect(transport)

  const [first, second] = await Promise.all([
    client.callTool({ name: 'get_comparison', arguments: {} }),
    secondClient.callTool({ name: 'get_comparison', arguments: {} }),
  ])

  expect(transport.sessionId).toBeUndefined()
  expect(second.structuredContent).toEqual(first.structuredContent)
})

test('rejects reads from a replaced Comparison Snapshot with a structured error', async () => {
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id
  await client.callTool({ name: 'refresh_comparison', arguments: {} })

  let replacementId = snapshotId
  for (let attempt = 0; attempt < 50 && replacementId === snapshotId; attempt++) {
    const current = await client.callTool({ name: 'get_comparison', arguments: {} })
    replacementId = (current.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id
    if (replacementId === snapshotId)
      await Bun.sleep(5)
  }
  expect(replacementId).not.toBe(snapshotId)

  const result = await client.callTool({
    name: 'list_changes',
    arguments: { snapshot_id: snapshotId },
  })

  expect(result.isError).toBe(true)
  expect(result.structuredContent).toEqual({
    error: {
      code: 'snapshot_changed',
      message: 'The requested Comparison Snapshot is no longer available',
    },
  })
})

test('maps an invalid list cursor to a structured tool error', async () => {
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id

  const result = await client.callTool({
    name: 'list_changes',
    arguments: { snapshot_id: snapshotId, cursor: 'not-a-cursor' },
  })

  expect(result.isError).toBe(true)
  expect(result.structuredContent).toEqual({
    error: { code: 'invalid_cursor', message: 'The cursor is invalid' },
  })
})

test('maps an unknown Entry ID to a structured tool error', async () => {
  const { client } = await startFixture()
  const comparison = await client.callTool({ name: 'get_comparison', arguments: {} })
  const snapshotId = (comparison.structuredContent as { snapshot: { snapshot_id: string } }).snapshot.snapshot_id

  const result = await client.callTool({
    name: 'get_text_diff',
    arguments: { snapshot_id: snapshotId, entry_id: 999 },
  })

  expect(result.isError).toBe(true)
  expect(result.structuredContent).toEqual({
    error: { code: 'entry_not_found', message: 'The Comparison Entry does not exist in this snapshot' },
  })
})
