import type {
  ComparisonEntry,
  ComparisonEntryKind,
  ComparisonIssue,
  ComparisonItemKind,
  ComparisonListEntry,
  ComparisonResultStatus,
  ComparisonSide,
  ComparisonSnapshot,
  ComparisonWorkspace,
  EntryDetail,
  RefreshRun,
} from './types'
import { createDiffMcpHandler } from './mcp'
import diffWebApp from './web/index.html'

export interface RunningDiffServer {
  readonly url: URL
  readonly finished: Promise<void>
  stop: () => Promise<void>
}

const API_HEADERS = {
  'Cache-Control': 'no-store',
  'Content-Security-Policy': 'default-src \'self\'; script-src \'self\'; style-src \'self\' \'unsafe-inline\'; worker-src \'self\'; img-src \'self\' blob: data:; object-src \'none\'; base-uri \'none\'; frame-ancestors \'none\'',
  'Referrer-Policy': 'no-referrer',
  'X-Content-Type-Options': 'nosniff',
}

function configureWebBundleHeaders(bundle: Bun.HTMLBundle): void {
  for (const file of bundle.files ?? []) {
    file.headers['referrer-policy'] = 'no-referrer'
    file.headers['x-content-type-options'] = 'nosniff'
    if (file.loader === 'html') {
      file.headers['cache-control'] = 'no-store'
      file.headers['content-security-policy'] = API_HEADERS['Content-Security-Policy']
    }
    else {
      file.headers['cache-control'] = 'public, max-age=31536000, immutable'
    }
  }
}

function json(data: unknown, status = 200): Response {
  return Response.json(data, { status, headers: API_HEADERS })
}

function error(code: string, message: string, status: number): Response {
  return json({ version: 1, error: { code, message } }, status)
}

function requireMethod(request: Request, method: string): Response | undefined {
  return request.method === method
    ? undefined
    : error('METHOD_NOT_ALLOWED', `Use ${method}`, 405)
}

function requestSnapshot(request: Request, workspace: ComparisonWorkspace): ComparisonSnapshot | Response {
  const id = new URL(request.url).searchParams.get('snapshot')
  const snapshot = id ? workspace.snapshot(id) : undefined
  return snapshot ?? error('SNAPSHOT_CHANGED', 'The requested Comparison Snapshot is no longer available', 409)
}

function entryId(request: Request): number | Response {
  const pathname = new URL(request.url).pathname
  const match = /^\/api\/entries\/(\d+)/.exec(pathname)
  const id = Number(match?.[1])
  return Number.isSafeInteger(id) && id > 0
    ? id
    : error('INVALID_REQUEST', 'Entry ID must be a positive integer', 400)
}

function comparisonSide(request: Request): ComparisonSide | Response {
  const pathname = new URL(request.url).pathname
  const side = pathname.split('/').at(-1)
  return side === 'baseline' || side === 'target'
    ? side
    : error('INVALID_REQUEST', 'Comparison side must be baseline or target', 400)
}

const COMPARISON_STATUSES = new Set<ComparisonResultStatus>(['added', 'deleted', 'modified', 'unchanged', 'issue'])
const ENTRY_KINDS = new Set<ComparisonItemKind>(['file', 'symlink', 'issue'])

function browserEntry(entry: ComparisonEntry): {
  id: number
  path: string
  status: ComparisonEntry['status']
  kind: ComparisonEntryKind
  baselineSize?: number
  targetSize?: number
} {
  return {
    id: entry.id,
    path: entry.path,
    status: entry.status,
    kind: entry.target?.kind ?? entry.baseline!.kind,
    ...(entry.baseline?.kind === 'file' ? { baselineSize: entry.baseline.size } : {}),
    ...(entry.target?.kind === 'file' ? { targetSize: entry.target.size } : {}),
  }
}

function browserListEntry(entry: ComparisonListEntry): ReturnType<typeof browserEntry> | ComparisonIssue {
  return entry.status === 'issue' ? entry : browserEntry(entry)
}

function browserEntryDetail(detail: EntryDetail): Record<string, unknown> {
  if (detail.status === 'issue')
    return { ...detail }
  return {
    ...browserEntry(detail),
    presentation: detail.presentation,
    ...(detail.baseline?.kind === 'symlink' ? { baselineLinkTarget: detail.baseline.linkTarget } : {}),
    ...(detail.target?.kind === 'symlink' ? { targetLinkTarget: detail.target.linkTarget } : {}),
  }
}

function listValues<T extends string>(query: URLSearchParams, name: string, allowed: Set<T>): T[] | Response {
  const values = query.getAll(name).flatMap(value => value.split(',')).filter(Boolean)
  return values.every((value): value is T => allowed.has(value as T))
    ? values
    : error('INVALID_REQUEST', `Invalid ${name} filter`, 400)
}

export function startDiffHttpServer(options: {
  workspace: ComparisonWorkspace
  address: string
  port: number
  initialRefresh?: boolean
}): RunningDiffServer {
  configureWebBundleHeaders(diffWebApp)
  let finish: (() => void) | undefined
  const finished = new Promise<void>((resolve) => {
    finish = resolve
  })
  let activeRefresh: RefreshRun | undefined
  let stopped = false
  const beginRefresh = (): RefreshRun => {
    const refresh = options.workspace.refresh()
    activeRefresh = refresh
    void refresh.result.then(
      () => {
        if (activeRefresh === refresh)
          activeRefresh = undefined
      },
      () => {
        if (activeRefresh === refresh)
          activeRefresh = undefined
      },
    )
    return refresh
  }
  const startRefresh = (): boolean => {
    if (activeRefresh || ['discovering', 'comparing', 'publishing'].includes(options.workspace.state().phase))
      return false
    beginRefresh()
    return true
  }
  const handleMcp = createDiffMcpHandler(options.workspace, startRefresh, API_HEADERS, options.address)
  const server = Bun.serve({
    hostname: options.address,
    port: options.port,
    routes: {
      '/mcp': handleMcp,
      '/api/state': request => requireMethod(request, 'GET') ?? json({
        version: 1,
        workspace: options.workspace.state(),
        snapshot: options.workspace.snapshot()?.summary,
      }),
      '/api/events': (request, bunServer) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        bunServer.timeout(request, 0)
        const encoder = new TextEncoder()
        let unsubscribe: (() => void) | undefined
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            unsubscribe = options.workspace.observe((state) => {
              controller.enqueue(encoder.encode(`data: ${JSON.stringify({
                version: 1,
                workspace: state,
                snapshot: options.workspace.snapshot()?.summary,
              })}\n\n`))
            })
          },
          cancel() {
            unsubscribe?.()
          },
        })
        return new Response(stream, {
          headers: {
            ...API_HEADERS,
            'Content-Type': 'text/event-stream; charset=utf-8',
          },
        })
      },
      '/api/refresh': (request) => {
        if (!['POST', 'DELETE'].includes(request.method))
          return error('METHOD_NOT_ALLOWED', 'Use POST or DELETE', 405)

        const origin = request.headers.get('Origin')
        if (origin && origin !== new URL(request.url).origin)
          return error('ORIGIN_FORBIDDEN', 'Refresh requests must be same-origin', 403)

        if (request.method === 'DELETE') {
          activeRefresh?.cancel()
          return new Response(null, { status: 204, headers: API_HEADERS })
        }
        if (!startRefresh())
          return error('REFRESH_ACTIVE', 'A refresh is already active', 409)
        return json({ accepted: true }, 202)
      },
      '/api/entries': (request) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        const snapshot = requestSnapshot(request, options.workspace)
        if (snapshot instanceof Response)
          return snapshot
        const query = new URL(request.url).searchParams
        try {
          const statuses = listValues(query, 'status', COMPARISON_STATUSES)
          const kinds = listValues(query, 'kind', ENTRY_KINDS)
          if (statuses instanceof Response)
            return statuses
          if (kinds instanceof Response)
            return kinds
          const rawLimit = query.get('limit')
          const limit = rawLimit === null ? undefined : Number(rawLimit)
          if (limit !== undefined && (!Number.isSafeInteger(limit) || limit < 1))
            return error('INVALID_REQUEST', 'limit must be a positive integer', 400)
          const rawAnchor = query.get('anchor')
          const anchor = rawAnchor === null ? undefined : Number(rawAnchor)
          if (anchor !== undefined && (!Number.isSafeInteger(anchor) || anchor < 1))
            return error('INVALID_REQUEST', 'anchor must be a positive integer', 400)
          const page = snapshot.list({
            cursor: query.get('cursor') ?? undefined,
            anchor,
            limit,
            includeUnchanged: query.get('includeUnchanged') === 'true',
            statuses,
            kinds,
            path: query.get('path') ?? undefined,
          })
          return json({ ...page, entries: page.entries.map(browserListEntry) })
        }
        catch (cause) {
          return error('INVALID_REQUEST', cause instanceof Error ? cause.message : String(cause), 400)
        }
      },
      '/api/tree': (request) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        const snapshot = requestSnapshot(request, options.workspace)
        if (snapshot instanceof Response)
          return snapshot
        const directory = new URL(request.url).searchParams.get('path') ?? ''
        return json(snapshot.tree({ path: directory }))
      },
      '/api/search': (request) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        const snapshot = requestSnapshot(request, options.workspace)
        if (snapshot instanceof Response)
          return snapshot
        const query = new URL(request.url).searchParams
        const search = query.get('q')?.trim() ?? ''
        if (!search)
          return json({ results: [], truncated: false })
        const statuses = listValues(query, 'status', COMPARISON_STATUSES)
        if (statuses instanceof Response)
          return statuses
        const rawLimit = query.get('limit')
        const limit = rawLimit === null ? 200 : Number(rawLimit)
        if (!Number.isSafeInteger(limit) || limit < 1 || limit > 200)
          return error('INVALID_REQUEST', 'limit must be an integer between 1 and 200', 400)
        return json(snapshot.search(search, statuses, limit))
      },
      '/api/entries/:id': async (request) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        const snapshot = requestSnapshot(request, options.workspace)
        if (snapshot instanceof Response)
          return snapshot
        const id = entryId(request)
        if (id instanceof Response)
          return id
        try {
          return json(browserEntryDetail(await snapshot.detail(id)))
        }
        catch (cause) {
          return error('ENTRY_NOT_FOUND', cause instanceof Error ? cause.message : String(cause), 404)
        }
      },
      '/api/entries/:id/content/:side': async (request) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        const snapshot = requestSnapshot(request, options.workspace)
        if (snapshot instanceof Response)
          return snapshot
        const id = entryId(request)
        const side = comparisonSide(request)
        if (id instanceof Response)
          return id
        if (side instanceof Response)
          return side
        try {
          const force = new URL(request.url).searchParams.get('force') === 'true'
          return json(await snapshot.content(id, side, force))
        }
        catch (cause) {
          return error('ENTRY_NOT_FOUND', cause instanceof Error ? cause.message : String(cause), 404)
        }
      },
      '/api/entries/:id/blob/:side': async (request) => {
        const invalidMethod = requireMethod(request, 'GET')
        if (invalidMethod)
          return invalidMethod
        const snapshot = requestSnapshot(request, options.workspace)
        if (snapshot instanceof Response)
          return snapshot
        const id = entryId(request)
        const side = comparisonSide(request)
        if (id instanceof Response)
          return id
        if (side instanceof Response)
          return side
        try {
          const blob = await snapshot.blob(id, side)
          if (blob.status !== 'ready') {
            const status = blob.status === 'stale' ? 409 : blob.status === 'missing' ? 404 : 415
            return error(blob.status.toUpperCase(), `Blob is ${blob.status}`, status)
          }
          const inline = blob.mimeType.startsWith('image/')
          const encodedFilename = encodeURIComponent(blob.filename).replace(/'/g, '%27')
          const headers = new Headers(API_HEADERS)
          headers.set('Content-Type', blob.mimeType)
          headers.set(
            'Content-Disposition',
            `${inline ? 'inline' : 'attachment'}; filename="${encodedFilename}"; filename*=UTF-8''${encodedFilename}`,
          )
          if (blob.mimeType === 'image/svg+xml')
            headers.set('Content-Security-Policy', 'sandbox; default-src \'none\'; style-src \'unsafe-inline\'')
          return new Response(Uint8Array.from(blob.bytes), { headers })
        }
        catch (cause) {
          return error('ENTRY_NOT_FOUND', cause instanceof Error ? cause.message : String(cause), 404)
        }
      },
      '/api/*': () => error('NOT_FOUND', 'API route not found', 404),
      '/*': diffWebApp,
    },
  })

  if (options.initialRefresh) {
    queueMicrotask(() => {
      if (!stopped && !activeRefresh && !['discovering', 'comparing', 'publishing'].includes(options.workspace.state().phase))
        beginRefresh()
    })
  }

  return {
    url: new URL(server.url),
    finished,
    async stop() {
      stopped = true
      activeRefresh?.cancel()
      await server.stop(true)
      finish?.()
    },
  }
}
