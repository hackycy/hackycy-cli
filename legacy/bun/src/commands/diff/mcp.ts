import type { ComparisonEntry, ComparisonEntryState, ComparisonWorkspace, TextDiffResult } from './types'
import { isIP } from 'node:net'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { WebStandardStreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js'
import { z } from 'zod/v4'

const changeStatusSchema = z.enum(['added', 'deleted', 'modified'])
const entryKindSchema = z.enum(['file', 'symlink'])
const entryStateSchema = z.union([
  z.object({ kind: z.literal('file'), size: z.number().int().nonnegative() }),
  z.object({ kind: z.literal('symlink'), link_target: z.string() }),
])
const changeSchema = z.object({
  entry_id: z.number().int().positive(),
  path: z.string(),
  status: changeStatusSchema,
  baseline: entryStateSchema.optional(),
  target: entryStateSchema.optional(),
})
const issueSchema = z.object({
  path: z.string(),
  message: z.string(),
})
const textEncodingSchema = z.enum(['utf-8', 'utf-16le', 'utf-16be'])
const textDiffSchema = z.object({
  status: z.enum(['ready', 'no_textual_changes', 'unavailable']),
  path: z.string(),
  comparison_status: changeStatusSchema,
  reason: z.enum([
    'encoding_or_bom_only',
    'non_text',
    'mixed_entry_kinds',
    'source_too_large',
    'stale',
    'complexity_limit',
    'output_too_large',
    'server_busy',
  ]).optional(),
  context_lines: z.number().int().min(0).max(20).optional(),
  baseline_encoding: textEncodingSchema.optional(),
  target_encoding: textEncodingSchema.optional(),
  baseline_size: z.number().int().nonnegative().optional(),
  baseline_line_count: z.number().int().nonnegative().optional(),
  target_size: z.number().int().nonnegative().optional(),
  target_line_count: z.number().int().nonnegative().optional(),
  added_lines: z.number().int().nonnegative().optional(),
  deleted_lines: z.number().int().nonnegative().optional(),
  output_bytes: z.number().int().nonnegative().optional(),
  patch: z.string().optional(),
})

function mcpEntryState(state: ComparisonEntryState): z.infer<typeof entryStateSchema> {
  return state.kind === 'file'
    ? state
    : { kind: 'symlink', link_target: state.linkTarget }
}

function mcpChange(entry: ComparisonEntry): z.infer<typeof changeSchema> {
  return {
    entry_id: entry.id,
    path: entry.path,
    status: entry.status as z.infer<typeof changeStatusSchema>,
    ...(entry.baseline ? { baseline: mcpEntryState(entry.baseline) } : {}),
    ...(entry.target ? { target: mcpEntryState(entry.target) } : {}),
  }
}

function mcpTextDiff(result: TextDiffResult): z.infer<typeof textDiffSchema> {
  if (result.status === 'ready') {
    return {
      status: result.status,
      path: result.path,
      comparison_status: result.comparisonStatus,
      context_lines: result.contextLines,
      ...(result.baselineEncoding ? { baseline_encoding: result.baselineEncoding } : {}),
      ...(result.targetEncoding ? { target_encoding: result.targetEncoding } : {}),
      added_lines: result.addedLines,
      deleted_lines: result.deletedLines,
      patch: result.patch,
    }
  }
  if (result.status === 'no_textual_changes') {
    return {
      status: result.status,
      path: result.path,
      comparison_status: result.comparisonStatus,
      reason: result.reason,
      baseline_encoding: result.baselineEncoding,
      target_encoding: result.targetEncoding,
    }
  }
  return {
    status: result.status,
    path: result.path,
    comparison_status: result.comparisonStatus,
    reason: result.reason,
    ...(result.baselineSize !== undefined ? { baseline_size: result.baselineSize } : {}),
    ...(result.baselineLineCount !== undefined ? { baseline_line_count: result.baselineLineCount } : {}),
    ...(result.targetSize !== undefined ? { target_size: result.targetSize } : {}),
    ...(result.targetLineCount !== undefined ? { target_line_count: result.targetLineCount } : {}),
    ...(result.addedLines !== undefined ? { added_lines: result.addedLines } : {}),
    ...(result.deletedLines !== undefined ? { deleted_lines: result.deletedLines } : {}),
    ...(result.outputBytes !== undefined ? { output_bytes: result.outputBytes } : {}),
  }
}

function toolError(code: string, message: string) {
  return {
    content: [{ type: 'text' as const, text: `${code}: ${message}` }],
    structuredContent: { error: { code, message } },
    isError: true,
  }
}

function createServer(_workspace: ComparisonWorkspace, startRefresh: () => boolean): McpServer {
  const server = new McpServer({ name: 'ycy-directory-diff', version: '1.0.0' })
  server.registerTool('get_comparison', {
    description: 'Return the diff service phase and current immutable Comparison Snapshot.',
    annotations: { readOnlyHint: true, openWorldHint: false },
    inputSchema: {},
    outputSchema: {
      phase: z.enum(['idle', 'discovering', 'comparing', 'publishing', 'ready', 'canceled', 'error']),
      error: z.string().optional(),
      snapshot: z.object({
        snapshot_id: z.string(),
        baseline_directory: z.string(),
        target_directory: z.string(),
        created_at: z.string(),
        counts: z.object({
          added: z.number().int().nonnegative(),
          deleted: z.number().int().nonnegative(),
          modified: z.number().int().nonnegative(),
          unchanged: z.number().int().nonnegative(),
        }),
        issues: z.number().int().nonnegative(),
      }).optional(),
    },
  }, async () => {
    const state = _workspace.state()
    const summary = _workspace.snapshot()?.summary
    const snapshot = summary
      ? {
          snapshot_id: summary.id,
          baseline_directory: summary.baselineDirectory,
          target_directory: summary.targetDirectory,
          created_at: summary.createdAt,
          counts: summary.counts,
          issues: summary.issues,
        }
      : undefined
    const changes = summary ? summary.counts.added + summary.counts.deleted + summary.counts.modified : 0
    return {
      content: [{
        type: 'text',
        text: summary
          ? `Comparison ${state.phase}: ${changes} ${changes === 1 ? 'change' : 'changes'}, ${summary.issues} ${summary.issues === 1 ? 'issue' : 'issues'}`
          : `Comparison ${state.phase}: no published snapshot`,
      }],
      structuredContent: {
        phase: state.phase,
        ...(state.error ? { error: state.error } : {}),
        ...(snapshot ? { snapshot } : {}),
      },
    }
  })

  server.registerTool('refresh_comparison', {
    description: 'Start an asynchronous Refresh of the fixed Comparison Workspace.',
    annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: false },
    inputSchema: {},
    outputSchema: {
      accepted: z.boolean(),
      already_running: z.boolean(),
    },
  }, async () => {
    const accepted = startRefresh()
    return {
      content: [{
        type: 'text',
        text: accepted ? 'Refresh accepted' : 'Refresh already running',
      }],
      structuredContent: {
        accepted,
        already_running: !accepted,
      },
    }
  })

  server.registerTool('list_changes', {
    description: 'List changed Comparison Entries from one immutable Comparison Snapshot. Errors include snapshot_changed and invalid_cursor.',
    annotations: { readOnlyHint: true, openWorldHint: false },
    inputSchema: {
      snapshot_id: z.string().min(1),
      statuses: z.array(changeStatusSchema).min(1).optional(),
      kinds: z.array(entryKindSchema).min(1).optional(),
      path: z.string().optional(),
      cursor: z.string().min(1).optional(),
      limit: z.number().int().min(1).max(500).default(100),
    },
    outputSchema: {
      changes: z.array(changeSchema),
      next_cursor: z.string().optional(),
    },
  }, async ({ snapshot_id, statuses, kinds, path, cursor, limit }) => {
    const snapshot = _workspace.snapshot(snapshot_id)
    if (!snapshot)
      return toolError('snapshot_changed', 'The requested Comparison Snapshot is no longer available')
    let page: ReturnType<typeof snapshot.list>
    try {
      page = snapshot.list({
        statuses: statuses ?? ['added', 'deleted', 'modified'],
        kinds,
        path,
        cursor,
        limit,
      })
    }
    catch (cause) {
      if (cause instanceof Error && cause.message === 'Invalid entry cursor')
        return toolError('invalid_cursor', 'The cursor is invalid')
      throw cause
    }
    const changes = page.entries.map((entry) => {
      if (entry.status === 'issue' || entry.status === 'unchanged')
        throw new Error('Comparison Snapshot returned a non-change entry')
      return mcpChange(entry)
    })
    return {
      content: [{
        type: 'text',
        text: `Listed ${changes.length} changed ${changes.length === 1 ? 'entry' : 'entries'}${page.nextCursor ? '; more available' : ''}`,
      }],
      structuredContent: {
        changes,
        ...(page.nextCursor ? { next_cursor: page.nextCursor } : {}),
      },
    }
  })

  server.registerTool('list_issues', {
    description: 'List Comparison Issues from one immutable Comparison Snapshot. Errors include snapshot_changed and invalid_cursor.',
    annotations: { readOnlyHint: true, openWorldHint: false },
    inputSchema: {
      snapshot_id: z.string().min(1),
      path: z.string().optional(),
      cursor: z.string().min(1).optional(),
      limit: z.number().int().min(1).max(500).default(100),
    },
    outputSchema: {
      issues: z.array(issueSchema),
      next_cursor: z.string().optional(),
    },
  }, async ({ snapshot_id, path, cursor, limit }) => {
    const snapshot = _workspace.snapshot(snapshot_id)
    if (!snapshot)
      return toolError('snapshot_changed', 'The requested Comparison Snapshot is no longer available')
    let page: ReturnType<typeof snapshot.list>
    try {
      page = snapshot.list({ statuses: ['issue'], path, cursor, limit })
    }
    catch (cause) {
      if (cause instanceof Error && cause.message === 'Invalid entry cursor')
        return toolError('invalid_cursor', 'The cursor is invalid')
      throw cause
    }
    const issues = page.entries.map((entry) => {
      if (entry.status !== 'issue')
        throw new Error('Comparison Snapshot returned a non-issue entry')
      return { path: entry.path, message: entry.message }
    })
    return {
      content: [{
        type: 'text',
        text: `Listed ${issues.length} Comparison ${issues.length === 1 ? 'Issue' : 'Issues'}${page.nextCursor ? '; more available' : ''}`,
      }],
      structuredContent: {
        issues,
        ...(page.nextCursor ? { next_cursor: page.nextCursor } : {}),
      },
    }
  })

  server.registerTool('search_changes', {
    description: 'Search changed Comparison Paths by case-insensitive substring without reading file content. Returns snapshot_changed when the snapshot was replaced.',
    annotations: { readOnlyHint: true, openWorldHint: false },
    inputSchema: {
      snapshot_id: z.string().min(1),
      query: z.string().trim().min(1),
      statuses: z.array(changeStatusSchema).min(1).optional(),
      kinds: z.array(entryKindSchema).min(1).optional(),
      limit: z.number().int().min(1).max(100).default(20),
    },
    outputSchema: {
      changes: z.array(changeSchema),
      truncated: z.boolean(),
    },
  }, async ({ snapshot_id, query, statuses, kinds, limit }) => {
    const snapshot = _workspace.snapshot(snapshot_id)
    if (!snapshot)
      return toolError('snapshot_changed', 'The requested Comparison Snapshot is no longer available')
    const page = snapshot.list({
      statuses: statuses ?? ['added', 'deleted', 'modified'],
      kinds,
      path: query,
      limit: limit + 1,
    })
    const entries = page.entries.slice(0, limit)
    const changes = entries.map((entry) => {
      if (entry.status === 'issue' || entry.status === 'unchanged')
        throw new Error('Comparison Snapshot returned a non-change entry')
      return mcpChange(entry)
    })
    const truncated = page.entries.length > limit || page.nextCursor !== undefined
    return {
      content: [{
        type: 'text',
        text: `Found ${changes.length} changed ${changes.length === 1 ? 'entry' : 'entries'}${truncated ? '; result truncated' : ''}`,
      }],
      structuredContent: { changes, truncated },
    }
  })

  server.registerTool('get_text_diff', {
    description: 'Generate a bounded analysis-only Unified Diff for one changed text Comparison Entry. Errors include snapshot_changed and entry_not_found.',
    annotations: { readOnlyHint: true, openWorldHint: false },
    inputSchema: {
      snapshot_id: z.string().min(1),
      entry_id: z.number().int().positive().safe(),
      context_lines: z.number().int().min(0).max(20).default(3),
    },
    outputSchema: textDiffSchema,
  }, async ({ snapshot_id, entry_id, context_lines }) => {
    const snapshot = _workspace.snapshot(snapshot_id)
    if (!snapshot)
      return toolError('snapshot_changed', 'The requested Comparison Snapshot is no longer available')
    let textDiff: TextDiffResult
    try {
      textDiff = await snapshot.textDiff(entry_id, { contextLines: context_lines })
    }
    catch (cause) {
      if (cause instanceof Error && cause.message === 'Comparison Entry not found') {
        return toolError(
          'entry_not_found',
          'The Comparison Entry does not exist in this snapshot',
        )
      }
      throw cause
    }
    const result = mcpTextDiff(textDiff)
    const text = result.status === 'ready'
      ? `Text Difference ready for ${result.path}: +${result.added_lines} -${result.deleted_lines}`
      : result.status === 'no_textual_changes'
        ? `No textual changes for ${result.path}`
        : `Text Difference unavailable for ${result.path}: ${result.reason}`
    return {
      content: [{ type: 'text', text }],
      structuredContent: result,
    }
  })

  return server
}

export function createDiffMcpHandler(
  workspace: ComparisonWorkspace,
  startRefresh: () => boolean,
  responseHeaders: HeadersInit,
  bindingAddress: string,
): (request: Request) => Promise<Response> {
  const withResponseHeaders = (response: Response): Response => {
    const headers = new Headers(response.headers)
    new Headers(responseHeaders).forEach((value, name) => headers.set(name, value))
    return new Response(response.body, {
      status: response.status,
      statusText: response.statusText,
      headers,
    })
  }

  return async (request) => {
    const origin = request.headers.get('Origin')
    const requestUrl = new URL(request.url)
    let originUrl: URL | undefined
    try {
      originUrl = origin ? new URL(origin) : undefined
    }
    catch {
      originUrl = undefined
    }
    const originHostname = originUrl?.hostname.replace(/^\[|\]$/g, '')
    const hostnameAllowed = originHostname !== undefined && (
      originHostname === 'localhost'
      || originHostname === bindingAddress
      || (bindingAddress === '0.0.0.0' && isIP(originHostname) !== 0)
    )
    if (origin && (
      !originUrl
      || origin !== originUrl.origin
      || originUrl.origin !== requestUrl.origin
      || !hostnameAllowed
    )) {
      return withResponseHeaders(Response.json({
        jsonrpc: '2.0',
        id: null,
        error: { code: -32000, message: 'MCP requests must be same-origin' },
      }, { status: 403 }))
    }
    const transport = new WebStandardStreamableHTTPServerTransport({
      enableJsonResponse: true,
      sessionIdGenerator: undefined,
    })
    const server = createServer(workspace, startRefresh)
    await server.connect(transport)
    return withResponseHeaders(await transport.handleRequest(request))
  }
}
