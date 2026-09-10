import type { FsAuthentication } from './authentication'
import type { FsChunkedUploadManager } from './chunked-upload-service'
import type { DownloadErrorCode } from './download-service'
import type { ExtractionErrorCode } from './extraction-service'
import type { FsDownloadManager, FsErrorCode, FsExtractionManager, FsWorkspace } from './types'
import { isIP } from 'node:net'
import { z } from 'zod'
import { FileSessionError } from '../../shared/file-session'
import { ChunkedUploadError, createChunkedUploadManager } from './chunked-upload-service'
import { createRemoteDownloadManager, DownloadError } from './download-service'
import { createExtractionManager, ExtractionError } from './extraction-service'
import { ThumbnailError, ThumbnailService } from './thumbnail-service'
import { FsWorkspaceError } from './types'
import fsWebApp from './web/index.html'
import { MAX_TEXT_PREVIEW_BYTES, MAX_UPLOAD_BYTES } from './workspace'

export interface RunningFsServer {
  readonly url: URL
  readonly finished: Promise<void>
  stop: () => Promise<void>
}

const API_HEADERS = {
  'Cache-Control': 'no-store',
  'Content-Security-Policy': 'default-src \'none\'; frame-ancestors \'none\'',
  'Referrer-Policy': 'no-referrer',
  'X-Content-Type-Options': 'nosniff',
}

const APP_CONTENT_SECURITY_POLICY = 'default-src \'self\'; script-src \'self\'; style-src \'self\' \'unsafe-inline\'; worker-src \'self\'; img-src \'self\' blob: data:; media-src \'self\'; frame-src \'self\'; connect-src \'self\'; object-src \'none\'; base-uri \'none\'; frame-ancestors \'none\''
const ACTIVE_FILE_CONTENT_SECURITY_POLICY = 'sandbox; default-src \'none\'; style-src \'unsafe-inline\'; img-src data:; object-src \'none\'; base-uri \'none\'; frame-ancestors \'none\''
const SESSION_COOKIE = 'ycy_fs_session'
const SESSION_COOKIE_ATTRIBUTES = 'HttpOnly; SameSite=Strict; Path=/'

const operationPathSchema = z.string().max(4096)
const operationPathsSchema = z.array(operationPathSchema).min(1).max(1000)
const operationSchema = z.discriminatedUnion('action', [
  z.object({ action: z.literal('create-directory'), parentPath: operationPathSchema, name: z.string().max(4096) }).strict(),
  z.object({ action: z.literal('rename'), path: operationPathSchema, newName: z.string().max(4096) }).strict(),
  z.object({ action: z.literal('copy'), paths: operationPathsSchema, destinationPath: operationPathSchema }).strict(),
  z.object({ action: z.literal('move'), paths: operationPathsSchema, destinationPath: operationPathSchema }).strict(),
  z.object({ action: z.literal('delete'), paths: operationPathsSchema }).strict(),
])

const downloadRequestSchema = z.object({
  url: z.string().max(8192),
  directoryPath: z.string().max(4096),
  filename: z.string().max(4096).optional(),
}).strict()

const extractionRequestSchema = z.object({
  paths: z.array(operationPathSchema).min(1).max(100),
}).strict()

const chunkedUploadRequestSchema = z.object({
  directoryPath: z.string().max(4096),
  filename: z.string().max(4096),
  size: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
}).strict()

const sessionSchema = z.object({
  username: z.string().max(64),
  password: z.string().max(256),
}).strict()

const CHUNKED_UPLOAD_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

function configureWebBundleHeaders(bundle: Bun.HTMLBundle): void {
  for (const file of bundle.files ?? []) {
    file.headers['referrer-policy'] = 'no-referrer'
    file.headers['x-content-type-options'] = 'nosniff'
    if (file.loader === 'html') {
      file.headers['cache-control'] = 'no-store'
      file.headers['content-security-policy'] = APP_CONTENT_SECURITY_POLICY
    }
    else {
      file.headers['cache-control'] = 'public, max-age=31536000, immutable'
    }
  }
}

const ERROR_STATUS: Record<FsErrorCode, number> = {
  INVALID_PATH: 400,
  INVALID_UPLOAD: 400,
  INVALID_NAME: 400,
  INVALID_OPERATION: 409,
  PATH_FORBIDDEN: 403,
  NOT_FOUND: 404,
  NOT_DIRECTORY: 409,
  NOT_FILE: 409,
  TOO_LARGE: 413,
  PRECONDITION_REQUIRED: 428,
  REVISION_MISMATCH: 412,
  UNSUPPORTED_TEXT: 409,
  NAME_EXHAUSTED: 409,
  ALREADY_EXISTS: 409,
  ROOT_IMMUTABLE: 409,
  UNAVAILABLE: 500,
  UNSUPPORTED_ARCHIVE: 409,
  INVALID_ARCHIVE: 409,
  ENCRYPTED_ARCHIVE: 409,
  INSUFFICIENT_SPACE: 507,
}

function json(data: unknown, status = 200, headers?: HeadersInit): Response {
  return Response.json(data, { status, headers: { ...API_HEADERS, ...headers } })
}

function error(code: string, message: string, status: number): Response {
  return json({ version: 1, error: { code, message } }, status)
}

function workspaceError(cause: unknown): Response {
  if (cause instanceof FsWorkspaceError)
    return error(cause.code, cause.message, ERROR_STATUS[cause.code])
  return error('INTERNAL_ERROR', cause instanceof Error ? cause.message : String(cause), 500)
}

const DOWNLOAD_ERROR_STATUS: Record<DownloadErrorCode, number> = {
  INVALID_DOWNLOAD: 400,
  URL_FORBIDDEN: 403,
  DOWNLOAD_NOT_FOUND: 404,
  DOWNLOAD_ACTIVE: 409,
  DOWNLOAD_QUEUE_FULL: 429,
  DOWNLOAD_UNAVAILABLE: 502,
  DOWNLOAD_SERVICE_STOPPED: 503,
}

function downloadError(cause: unknown): Response {
  if (cause instanceof DownloadError)
    return error(cause.code, cause.message, DOWNLOAD_ERROR_STATUS[cause.code])
  return error('DOWNLOAD_UNAVAILABLE', cause instanceof Error ? cause.message : String(cause), 502)
}

const EXTRACTION_ERROR_STATUS: Record<ExtractionErrorCode, number> = {
  INVALID_EXTRACTION: 400,
  EXTRACTION_NOT_FOUND: 404,
  EXTRACTION_ACTIVE: 409,
  EXTRACTION_QUEUE_FULL: 429,
  EXTRACTION_SERVICE_STOPPED: 503,
}

function extractionError(cause: unknown): Response {
  if (cause instanceof ExtractionError)
    return error(cause.code, cause.message, EXTRACTION_ERROR_STATUS[cause.code])
  return error('EXTRACTION_UNAVAILABLE', cause instanceof Error ? cause.message : String(cause), 500)
}

const CHUNKED_UPLOAD_ERROR_STATUS: Record<ChunkedUploadError['code'], number> = {
  INVALID_CHUNKED_UPLOAD: 400,
  CHUNKED_UPLOAD_NOT_FOUND: 404,
  CHUNKED_UPLOAD_OFFSET_MISMATCH: 409,
  CHUNKED_UPLOAD_ACTIVE: 409,
  CHUNKED_UPLOAD_INCOMPLETE: 409,
  CHUNKED_UPLOAD_LIMIT_REACHED: 429,
  CHUNKED_UPLOAD_TIMEOUT: 408,
  CHUNKED_UPLOAD_STOPPED: 503,
}

const MAX_CHUNKED_UPLOAD_REQUEST_BYTES = 64 * 1024

class RequestBodyTooLarge extends Error {}

async function boundedJsonBody(request: Request, maxBytes: number): Promise<unknown> {
  const contentLength = request.headers.get('Content-Length')
  if (contentLength !== null) {
    const length = Number(contentLength)
    if (!Number.isSafeInteger(length) || length < 0 || length > maxBytes) {
      await request.body?.cancel().catch(() => {})
      throw new RequestBodyTooLarge()
    }
  }
  const reader = request.body?.getReader()
  if (!reader)
    throw new SyntaxError('Request body is missing')
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    while (true) {
      const chunk = await reader.read()
      if (chunk.done)
        break
      size += chunk.value.byteLength
      if (size > maxBytes) {
        await reader.cancel()
        throw new RequestBodyTooLarge()
      }
      chunks.push(chunk.value)
    }
  }
  finally {
    reader.releaseLock()
  }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }
  return JSON.parse(new TextDecoder().decode(bytes))
}

function chunkedUploadError(cause: unknown): Response {
  if (cause instanceof ChunkedUploadError)
    return error(cause.code, cause.message, CHUNKED_UPLOAD_ERROR_STATUS[cause.code])
  return workspaceError(cause)
}

function requireMethod(request: Request, method: string): Response | undefined {
  return request.method === method ? undefined : error('METHOD_NOT_ALLOWED', `Use ${method}`, 405)
}

function chunkedUploadPart(request: Request): { start: number, end: number, total: number } | undefined {
  const value = request.headers.get('Content-Range')
  const match = value && /^bytes (\d+)-(\d+)\/(\d+)$/.exec(value)
  if (!match)
    return undefined
  const start = Number(match[1])
  const end = Number(match[2])
  const total = Number(match[3])
  const contentLength = Number(request.headers.get('Content-Length'))
  if (![start, end, total, contentLength].every(Number.isSafeInteger) || start < 0 || end < start || total <= end || contentLength !== end - start + 1)
    return undefined
  return { start, end, total }
}

function sessionToken(request: Request): string | undefined {
  return request.headers.get('Cookie')
    ?.split(';')
    .map(value => value.trim())
    .find(value => value.startsWith(`${SESSION_COOKIE}=`))
    ?.slice(SESSION_COOKIE.length + 1)
}

function activeSessionCookie(token: string, expiresAt: string): string {
  const maxAge = Math.max(1, Math.ceil((Date.parse(expiresAt) - Date.now()) / 1000))
  return `${SESSION_COOKIE}=${token}; ${SESSION_COOKIE_ATTRIBUTES}; Max-Age=${maxAge}`
}

function expiredSessionCookie(): string {
  return `${SESSION_COOKIE}=; ${SESSION_COOKIE_ATTRIBUTES}; Max-Age=0`
}

function protectedPath(pathname: string): boolean {
  return pathname === '/api'
    || pathname.startsWith('/api/')
    || pathname === '/files'
    || pathname.startsWith('/files/')
    || pathname === '/thumbnails'
    || pathname.startsWith('/thumbnails/')
}

function bindingAllowsHostname(bindingAddress: string, hostname: string): boolean {
  if (bindingAddress === '0.0.0.0')
    return hostname === 'localhost' || isIP(hostname) === 4
  if (bindingAddress === '::')
    return hostname === 'localhost' || isIP(hostname) !== 0
  if (bindingAddress === '127.0.0.1' || bindingAddress === '::1')
    return hostname === bindingAddress || hostname === 'localhost'
  return hostname === bindingAddress
}

function validateMutationOrigin(request: Request, bindingAddress: string): Response | undefined {
  const requestUrl = new URL(request.url)
  const origin = request.headers.get('Origin')
  if (!origin || origin !== requestUrl.origin || !bindingAllowsHostname(bindingAddress, requestUrl.hostname))
    return error('ORIGIN_FORBIDDEN', 'Mutation requests must come from the bound same origin', 403)
  return undefined
}

function encodedPath(relativePath: string): string {
  return relativePath.split('/').filter(Boolean).map(encodeURIComponent).join('/')
}

function requestResourcePath(pathname: string, prefix: string): string {
  const encoded = pathname === prefix ? '' : pathname.slice(prefix.length + 1)
  try {
    return encoded.split('/').filter(Boolean).map(decodeURIComponent).join('/')
  }
  catch {
    throw new FsWorkspaceError('INVALID_PATH', 'File path is not valid URL encoding')
  }
}

function inlineMimeType(mimeType: string): boolean {
  const base = mimeType.split(';')[0]!.trim().toLowerCase()
  const [type, subtype] = base.split('/')
  if (!type || !subtype)
    return false
  if (['text', 'image', 'video', 'audio'].includes(type))
    return true
  return type === 'application' && ['pdf', 'json', 'xml', 'javascript', 'xhtml+xml', 'ld+json'].includes(subtype)
}

function htmlMimeType(mimeType: string): boolean {
  return ['application/xhtml+xml', 'text/html'].includes(mimeType.split(';')[0]!.trim().toLowerCase())
}

function requiresDocumentSandbox(mimeType: string, safeHtml: boolean): boolean {
  const baseMimeType = mimeType.split(';')[0]!.trim().toLowerCase()
  if (!safeHtml && htmlMimeType(mimeType))
    return false
  return [
    'application/xhtml+xml',
    'application/xml',
    'image/svg+xml',
    'text/html',
    'text/xml',
  ].includes(baseMimeType)
}

function contentDisposition(filename: string, inline: boolean): string {
  const encoded = encodeURIComponent(filename).replace(/'/g, '%27')
  return `${inline ? 'inline' : 'attachment'}; filename="${encoded}"; filename*=UTF-8''${encoded}`
}

function isNotModified(request: Request, etag: string, modifiedAt: Date): boolean {
  const ifNoneMatch = request.headers.get('If-None-Match')
  if (ifNoneMatch)
    return ifNoneMatch.split(',').map(value => value.trim()).some(value => value === '*' || value === etag)
  const ifModifiedSince = request.headers.get('If-Modified-Since')
  if (!ifModifiedSince)
    return false
  const timestamp = Date.parse(ifModifiedSince)
  return Number.isFinite(timestamp) && modifiedAt.getTime() < timestamp + 1000
}

function parseByteRange(value: string, size: number): { start: number, end: number } | undefined {
  if (value.includes(','))
    return undefined
  const match = /^bytes=(\d*)-(\d*)$/.exec(value.trim())
  if (!match || (!match[1] && !match[2]) || size === 0)
    return undefined

  if (!match[1]) {
    const suffixLength = Number(match[2])
    if (!Number.isSafeInteger(suffixLength) || suffixLength <= 0)
      return undefined
    return { start: Math.max(size - suffixLength, 0), end: size - 1 }
  }

  const start = Number(match[1])
  const requestedEnd = match[2] ? Number(match[2]) : size - 1
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(requestedEnd) || start < 0 || start >= size || requestedEnd < start)
    return undefined
  return { start, end: Math.min(requestedEnd, size - 1) }
}

function rangeAllowed(request: Request, etag: string, modifiedAt: Date): boolean {
  const ifRange = request.headers.get('If-Range')
  if (!ifRange)
    return true
  if (ifRange.startsWith('W/'))
    return false
  if (ifRange.startsWith('"'))
    return ifRange === etag
  const timestamp = Date.parse(ifRange)
  return Number.isFinite(timestamp) && modifiedAt.getTime() < timestamp + 1000
}

async function streamOriginalFile(request: Request, workspace: FsWorkspace, corsEnabled: boolean, safeHtml: boolean): Promise<Response> {
  if (request.method === 'OPTIONS') {
    return new Response(null, {
      status: 204,
      headers: corsEnabled
        ? {
            'Access-Control-Allow-Headers': 'Range, If-None-Match, If-Modified-Since, If-Range',
            'Access-Control-Allow-Methods': 'GET, HEAD, OPTIONS',
            'Access-Control-Allow-Origin': '*',
            'Access-Control-Max-Age': '86400',
          }
        : undefined,
    })
  }
  if (!['GET', 'HEAD'].includes(request.method))
    return error('METHOD_NOT_ALLOWED', 'Use GET or HEAD', 405)
  const url = new URL(request.url)
  let relativePath: string
  try {
    relativePath = requestResourcePath(url.pathname, '/files')
  }
  catch (cause) {
    return workspaceError(cause)
  }

  try {
    const file = await workspace.openFile(relativePath)
    const forceDownload = url.searchParams.get('download') === '1' || (safeHtml && htmlMimeType(file.mimeType))
    const etag = `W/"${file.size}-${file.modifiedAt.getTime()}"`
    const headers = new Headers({
      'Accept-Ranges': 'bytes',
      'Cache-Control': 'no-cache',
      'Content-Disposition': contentDisposition(file.name, !forceDownload && inlineMimeType(file.mimeType)),
      'Content-Length': String(file.size),
      'Content-Type': file.mimeType,
      'ETag': etag,
      'Last-Modified': file.modifiedAt.toUTCString(),
      'Referrer-Policy': 'no-referrer',
      'X-Content-Type-Options': 'nosniff',
    })
    if (corsEnabled)
      headers.set('Access-Control-Allow-Origin', '*')
    if (requiresDocumentSandbox(file.mimeType, safeHtml))
      headers.set('Content-Security-Policy', ACTIVE_FILE_CONTENT_SECURITY_POLICY)

    if (isNotModified(request, etag, file.modifiedAt)) {
      headers.delete('Content-Length')
      return new Response(null, { status: 304, headers })
    }

    const rangeHeader = request.headers.get('Range')
    const useRange = rangeHeader && rangeAllowed(request, etag, file.modifiedAt)
    if (useRange) {
      const range = parseByteRange(rangeHeader, file.size)
      if (!range) {
        headers.set('Content-Range', `bytes */${file.size}`)
        headers.delete('Content-Length')
        return new Response(null, { status: 416, headers })
      }
      headers.set('Content-Range', `bytes ${range.start}-${range.end}/${file.size}`)
      headers.set('Content-Length', String(range.end - range.start + 1))
      return new Response(request.method === 'HEAD' ? null : file.body.slice(range.start, range.end + 1), {
        status: 206,
        headers,
      })
    }
    const body = request.method === 'HEAD'
      ? null
      : rangeHeader ? file.body.stream() : file.body
    return new Response(body, { headers })
  }
  catch (cause) {
    if (cause instanceof FsWorkspaceError && cause.code === 'NOT_FILE') {
      try {
        const listing = await workspace.listDirectory(relativePath)
        const location = listing.path ? `/browse/${encodedPath(listing.path)}` : '/'
        return new Response(null, { status: 302, headers: { Location: location } })
      }
      catch (directoryCause) {
        return workspaceError(directoryCause)
      }
    }
    return workspaceError(cause)
  }
}

async function streamThumbnail(request: Request, thumbnails: ThumbnailService): Promise<Response> {
  if (!['GET', 'HEAD'].includes(request.method))
    return error('METHOD_NOT_ALLOWED', 'Use GET or HEAD', 405)

  let relativePath: string
  try {
    relativePath = requestResourcePath(new URL(request.url).pathname, '/thumbnails')
  }
  catch (cause) {
    return workspaceError(cause)
  }

  try {
    const thumbnail = await thumbnails.get(relativePath)
    const headers = new Headers({
      'Cache-Control': 'no-cache',
      'Content-Length': String(thumbnail.bytes.byteLength),
      'Content-Type': 'image/webp',
      'ETag': thumbnail.etag,
      'Last-Modified': thumbnail.modifiedAt.toUTCString(),
      'Referrer-Policy': 'no-referrer',
      'X-Content-Type-Options': 'nosniff',
    })
    if (isNotModified(request, thumbnail.etag, thumbnail.modifiedAt)) {
      headers.delete('Content-Length')
      return new Response(null, { status: 304, headers })
    }
    return new Response(request.method === 'HEAD' ? null : thumbnail.bytes, { headers })
  }
  catch (cause) {
    if (cause instanceof ThumbnailError)
      return error('THUMBNAIL_ERROR', cause.message, cause.status)
    return workspaceError(cause)
  }
}

export function startFsHttpServer(options: {
  workspace: FsWorkspace
  address: string
  port: number
  managementEnabled: boolean
  safeHtml?: boolean
  authentication?: FsAuthentication
  chunkedUpload?: { enabled: boolean, chunkSizeBytes: number }
  thumbnailService?: ThumbnailService
  downloadManager?: FsDownloadManager
  extractionManager?: FsExtractionManager
  chunkedUploadManager?: FsChunkedUploadManager
}): RunningFsServer {
  configureWebBundleHeaders(fsWebApp)
  const thumbnails = options.thumbnailService ?? new ThumbnailService(options.workspace)
  const downloads = options.downloadManager ?? createRemoteDownloadManager(options.workspace)
  const extractions = options.extractionManager ?? createExtractionManager(options.workspace)
  const chunkedUploads = options.chunkedUpload?.enabled
    ? options.chunkedUploadManager ?? createChunkedUploadManager(options.workspace, { chunkSizeBytes: options.chunkedUpload.chunkSizeBytes })
    : undefined
  // Bun accepts an HTMLBundle for a method route, though its ambient type only lists it for whole-route values.
  const appRoute = {
    GET: fsWebApp as unknown as Response,
    POST: () => error('METHOD_NOT_ALLOWED', 'Use GET', 405),
    PUT: () => error('METHOD_NOT_ALLOWED', 'Use GET', 405),
    DELETE: () => error('METHOD_NOT_ALLOWED', 'Use GET', 405),
    PATCH: () => error('METHOD_NOT_ALLOWED', 'Use GET', 405),
    HEAD: () => error('METHOD_NOT_ALLOWED', 'Use GET', 405),
    OPTIONS: () => error('METHOD_NOT_ALLOWED', 'Use GET', 405),
  }
  let finish: (() => void) | undefined
  const finished = new Promise<void>((resolve) => {
    finish = resolve
  })
  const server = Bun.serve({
    hostname: options.address,
    port: options.port,
    maxRequestBodySize: MAX_UPLOAD_BYTES + 1024 * 1024,
    routes: {
      '/': appRoute,
      '/browse': appRoute,
      '/browse/*': appRoute,
    },
    async fetch(request, bunServer) {
      const url = new URL(request.url)
      const token = sessionToken(request)
      let refreshedSession: { expiresAt: string } | undefined
      let response: Response
      try {
        response = await (async (): Promise<Response> => {
          if (url.pathname === '/api/session') {
            if (request.method === 'GET') {
              if (!options.authentication) {
                return json(
                  { version: 1, authenticationEnabled: false },
                  200,
                  token ? { 'Set-Cookie': expiredSessionCookie() } : undefined,
                )
              }
              const session = options.authentication.resume(token)
              refreshedSession = session
              return json(
                {
                  version: 1,
                  authenticationEnabled: true,
                  authenticated: session !== undefined,
                  ...(session ? { account: session.account } : {}),
                },
                200,
                token && !session ? { 'Set-Cookie': expiredSessionCookie() } : undefined,
              )
            }
            if (request.method === 'POST') {
              if (!options.authentication)
                return error('AUTHENTICATION_DISABLED', 'No accounts are configured for this server', 403)
              const invalidOrigin = validateMutationOrigin(request, options.address)
              if (invalidOrigin)
                return invalidOrigin
              if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json')
                return error('UNSUPPORTED_MEDIA_TYPE', 'Login requests must use JSON', 415)
              let body: unknown
              try {
                body = await request.json()
              }
              catch {
                return error('INVALID_REQUEST', 'Login request must be valid JSON', 400)
              }
              const parsed = sessionSchema.safeParse(body)
              if (!parsed.success)
                return error('INVALID_REQUEST', 'Login request is invalid', 400)
              const grant = await options.authentication.signIn(parsed.data)
              if (!grant)
                return error('AUTHENTICATION_FAILED', 'Account credentials are invalid', 401)
              return json(
                { version: 1, authenticationEnabled: true, authenticated: true, account: grant.account },
                200,
                { 'Set-Cookie': activeSessionCookie(grant.token, grant.expiresAt) },
              )
            }
            if (request.method === 'DELETE') {
              const invalidOrigin = validateMutationOrigin(request, options.address)
              if (invalidOrigin)
                return invalidOrigin
              options.authentication?.signOut(token)
              return new Response(null, { status: 204, headers: { ...API_HEADERS, 'Set-Cookie': expiredSessionCookie() } })
            }
            return error('METHOD_NOT_ALLOWED', 'Use GET, POST, or DELETE', 405)
          }

          if (options.authentication && protectedPath(url.pathname)) {
            refreshedSession = options.authentication.resume(token)
            if (!refreshedSession)
              return error('AUTHENTICATION_REQUIRED', 'Authenticated session is required', 401)
          }

          if (url.pathname === '/api/directory') {
            const invalidMethod = requireMethod(request, 'GET')
            if (invalidMethod)
              return invalidMethod
            try {
              const listing = await options.workspace.listDirectory(url.searchParams.get('path') ?? '')
              return json({
                version: 1,
                ...listing,
                managementEnabled: options.managementEnabled,
                maxUploadBytes: MAX_UPLOAD_BYTES,
                ...(options.managementEnabled && chunkedUploads
                  ? { chunkedUpload: { thresholdBytes: 20 * 1024 * 1024, chunkSizeBytes: options.chunkedUpload!.chunkSizeBytes } }
                  : {}),
              })
            }
            catch (cause) {
              return workspaceError(cause)
            }
          }
          if (url.pathname === '/api/text') {
            if (request.method === 'PUT') {
              if (!options.managementEnabled)
                return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable filesystem management', 403)
              const invalidOrigin = validateMutationOrigin(request, options.address)
              if (invalidOrigin)
                return invalidOrigin
              const contentType = request.headers.get('Content-Type')
              const [mediaType, ...parameters] = contentType?.split(';').map(value => value.trim().toLowerCase()) ?? []
              if (mediaType !== 'text/plain' || parameters.some(parameter => parameter !== 'charset=utf-8'))
                return error('UNSUPPORTED_MEDIA_TYPE', 'Text saves must use UTF-8 text/plain', 415)
              const expectedRevision = request.headers.get('If-Match')
              if (!expectedRevision)
                return error('PRECONDITION_REQUIRED', 'If-Match is required when saving text', 428)
              let body: string
              try {
                const length = Number(request.headers.get('Content-Length'))
                if (Number.isSafeInteger(length) && length > MAX_TEXT_PREVIEW_BYTES)
                  return error('TOO_LARGE', 'Edited text exceeds the 10 MiB limit', 413)
                const reader = request.body?.getReader()
                if (!reader) {
                  body = ''
                }
                else {
                  const chunks: Uint8Array[] = []
                  let size = 0
                  while (true) {
                    const chunk = await reader.read()
                    if (chunk.done)
                      break
                    size += chunk.value.byteLength
                    if (size > MAX_TEXT_PREVIEW_BYTES) {
                      await reader.cancel()
                      return error('TOO_LARGE', 'Edited text exceeds the 10 MiB limit', 413)
                    }
                    chunks.push(chunk.value)
                  }
                  const bytes = new Uint8Array(size)
                  let offset = 0
                  for (const chunk of chunks) {
                    bytes.set(chunk, offset)
                    offset += chunk.byteLength
                  }
                  body = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
                }
              }
              catch {
                return error('INVALID_REQUEST', 'Text save body must be valid UTF-8', 400)
              }
              try {
                return json({ version: 1, ...await options.workspace.saveTextFile(url.searchParams.get('path') ?? '', body, expectedRevision) })
              }
              catch (cause) {
                return workspaceError(cause)
              }
            }
            const invalidMethod = requireMethod(request, 'GET')
            if (invalidMethod)
              return invalidMethod
            try {
              return json({
                version: 1,
                ...await options.workspace.readTextPreview(url.searchParams.get('path') ?? ''),
              })
            }
            catch (cause) {
              return workspaceError(cause)
            }
          }
          if (url.pathname === '/api/upload') {
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable filesystem management', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'multipart/form-data')
              return error('UNSUPPORTED_MEDIA_TYPE', 'Upload requests must use multipart form data', 415)
            let file: FormDataEntryValue | null
            try {
              file = (await request.formData()).get('file')
            }
            catch {
              return error('INVALID_UPLOAD', 'Request body must be multipart form data', 400)
            }
            if (!(file instanceof File))
              return error('INVALID_UPLOAD', 'A file field is required', 400)
            try {
              return json({
                version: 1,
                ...await options.workspace.uploadFile(url.searchParams.get('path') ?? '', file),
              })
            }
            catch (cause) {
              return workspaceError(cause)
            }
          }
          if (url.pathname === '/api/uploads') {
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable filesystem management', 403)
            if (!chunkedUploads)
              return error('CHUNKED_UPLOAD_DISABLED', 'Start fs with --chunked-upload to enable chunked uploads', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json')
              return error('UNSUPPORTED_MEDIA_TYPE', 'Upload session requests must use JSON', 415)
            let body: unknown
            try {
              body = await boundedJsonBody(request, MAX_CHUNKED_UPLOAD_REQUEST_BYTES)
            }
            catch (cause) {
              if (cause instanceof RequestBodyTooLarge)
                return error('TOO_LARGE', 'Upload session request is too large', 413)
              return error('INVALID_CHUNKED_UPLOAD', 'Upload session request must be valid JSON', 400)
            }
            const parsed = chunkedUploadRequestSchema.safeParse(body)
            if (!parsed.success)
              return error('INVALID_CHUNKED_UPLOAD', 'Upload session request is invalid', 400)
            try {
              return json({ version: 1, upload: await chunkedUploads.create(parsed.data, token ?? 'anonymous') }, 201)
            }
            catch (cause) {
              return chunkedUploadError(cause)
            }
          }
          const chunkedUploadRoute = /^\/api\/uploads\/([^/]+)(?:\/(complete))?$/.exec(url.pathname)
          if (chunkedUploadRoute) {
            const id = chunkedUploadRoute[1]!
            const action = chunkedUploadRoute[2]
            if (!CHUNKED_UPLOAD_ID.test(id))
              return error('INVALID_CHUNKED_UPLOAD', 'Upload session ID is invalid', 400)
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable filesystem management', 403)
            if (!chunkedUploads)
              return error('CHUNKED_UPLOAD_DISABLED', 'Start fs with --chunked-upload to enable chunked uploads', 403)
            if (request.method !== 'GET') {
              const invalidOrigin = validateMutationOrigin(request, options.address)
              if (invalidOrigin)
                return invalidOrigin
            }
            try {
              if (!action && request.method === 'GET')
                return json({ version: 1, upload: await chunkedUploads.get(id, token ?? 'anonymous') })
              if (!action && request.method === 'DELETE') {
                await chunkedUploads.cancel(id, token ?? 'anonymous')
                return new Response(null, { status: 204, headers: API_HEADERS })
              }
              if (!action && request.method === 'PUT') {
                if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/octet-stream') {
                  await request.body?.cancel().catch(() => {})
                  return error('UNSUPPORTED_MEDIA_TYPE', 'Upload chunks must use application/octet-stream', 415)
                }
                const part = chunkedUploadPart(request)
                if (!part || !request.body) {
                  await request.body?.cancel().catch(() => {})
                  return error('INVALID_CHUNKED_UPLOAD', 'Upload chunk range or body is invalid', 400)
                }
                return json({ version: 1, upload: await chunkedUploads.write(id, token ?? 'anonymous', part, request.body, request.signal) })
              }
              if (action === 'complete' && request.method === 'POST')
                return json({ version: 1, upload: await chunkedUploads.complete(id, token ?? 'anonymous') })
              return error('METHOD_NOT_ALLOWED', action ? 'Use POST' : 'Use GET, PUT, or DELETE', 405)
            }
            catch (cause) {
              if (!action && request.method === 'PUT')
                await request.body?.cancel().catch(() => {})
              return chunkedUploadError(cause)
            }
          }
          if (url.pathname === '/api/operations') {
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable filesystem management', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json')
              return error('UNSUPPORTED_MEDIA_TYPE', 'Filesystem operations must use JSON', 415)
            let body: unknown
            try {
              body = await request.json()
            }
            catch {
              return error('INVALID_OPERATION', 'Request body must be valid JSON', 400)
            }
            const operation = operationSchema.safeParse(body)
            if (!operation.success)
              return error('INVALID_OPERATION', 'Filesystem operation is invalid', 400)
            return json({
              version: 1,
              ...await options.workspace.applyOperation(operation.data),
            })
          }
          if (url.pathname === '/api/downloads/events') {
            const invalidMethod = requireMethod(request, 'GET')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable remote downloads', 403)
            bunServer.timeout(request, 0)
            const encoder = new TextEncoder()
            let unsubscribe: (() => void) | undefined
            let stopObservingSession: (() => void) | undefined
            let closed = false
            const dispose = (): void => {
              unsubscribe?.()
              stopObservingSession?.()
              unsubscribe = undefined
              stopObservingSession = undefined
            }
            const stream = new ReadableStream<Uint8Array>({
              start(controller) {
                unsubscribe = downloads.subscribe((tasks) => {
                  controller.enqueue(encoder.encode(`data: ${JSON.stringify({ version: 1, tasks })}\n\n`))
                })
                if (options.authentication && token) {
                  stopObservingSession = options.authentication.observe(token, () => {
                    if (closed)
                      return
                    closed = true
                    dispose()
                    controller.close()
                  })
                }
              },
              cancel() {
                closed = true
                dispose()
              },
            })
            return new Response(stream, {
              headers: {
                ...API_HEADERS,
                'Cache-Control': 'no-cache',
                'Connection': 'keep-alive',
                'Content-Type': 'text/event-stream; charset=utf-8',
                'X-Accel-Buffering': 'no',
              },
            })
          }
          if (url.pathname === '/api/downloads') {
            if (request.method === 'GET') {
              if (!options.managementEnabled)
                return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable remote downloads', 403)
              return json({ version: 1, tasks: downloads.list() })
            }
            if (request.method === 'DELETE') {
              if (!options.managementEnabled)
                return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable remote downloads', 403)
              const invalidOrigin = validateMutationOrigin(request, options.address)
              if (invalidOrigin)
                return invalidOrigin
              if (url.searchParams.get('terminal') !== '1')
                return error('INVALID_DOWNLOAD', 'Use terminal=1 to clear completed downloads', 400)
              downloads.clearTerminal()
              return new Response(null, { status: 204, headers: API_HEADERS })
            }
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable remote downloads', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json')
              return error('UNSUPPORTED_MEDIA_TYPE', 'Download requests must use JSON', 415)
            let body: unknown
            try {
              body = await request.json()
            }
            catch {
              return error('INVALID_DOWNLOAD', 'Download request must be valid JSON', 400)
            }
            const parsed = downloadRequestSchema.safeParse(body)
            if (!parsed.success)
              return error('INVALID_DOWNLOAD', 'Download request is invalid', 400)
            try {
              await options.workspace.listDirectory(parsed.data.directoryPath)
              const task = await downloads.enqueue(parsed.data)
              return json({ version: 1, task }, 202)
            }
            catch (cause) {
              return cause instanceof DownloadError ? downloadError(cause) : workspaceError(cause)
            }
          }
          const downloadAction = /^\/api\/downloads\/([^/]+)\/(cancel|retry)$/.exec(url.pathname)
          if (downloadAction) {
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable remote downloads', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            let taskId: string
            try {
              taskId = decodeURIComponent(downloadAction[1]!)
            }
            catch {
              return error('INVALID_DOWNLOAD', 'Download task ID is invalid', 400)
            }
            try {
              const task = downloadAction[2] === 'cancel'
                ? downloads.cancel(taskId)
                : await downloads.retry(taskId)
              if (!task)
                return downloadError(new DownloadError('DOWNLOAD_NOT_FOUND', 'Download task was not found'))
              return json({ version: 1, task }, downloadAction[2] === 'retry' ? 202 : 200)
            }
            catch (cause) {
              return downloadError(cause)
            }
          }
          if (url.pathname === '/api/extractions/events') {
            const invalidMethod = requireMethod(request, 'GET')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable archive extraction', 403)
            bunServer.timeout(request, 0)
            const encoder = new TextEncoder()
            let unsubscribe: (() => void) | undefined
            let stopObservingSession: (() => void) | undefined
            let closed = false
            const dispose = (): void => {
              unsubscribe?.()
              stopObservingSession?.()
              unsubscribe = undefined
              stopObservingSession = undefined
            }
            const stream = new ReadableStream<Uint8Array>({
              start(controller) {
                unsubscribe = extractions.subscribe((tasks) => {
                  controller.enqueue(encoder.encode(`data: ${JSON.stringify({ version: 1, tasks })}\n\n`))
                })
                if (options.authentication && token) {
                  stopObservingSession = options.authentication.observe(token, () => {
                    if (closed)
                      return
                    closed = true
                    dispose()
                    controller.close()
                  })
                }
              },
              cancel() {
                closed = true
                dispose()
              },
            })
            return new Response(stream, {
              headers: {
                ...API_HEADERS,
                'Cache-Control': 'no-cache',
                'Connection': 'keep-alive',
                'Content-Type': 'text/event-stream; charset=utf-8',
                'X-Accel-Buffering': 'no',
              },
            })
          }
          if (url.pathname === '/api/extractions') {
            if (request.method === 'GET') {
              if (!options.managementEnabled)
                return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable archive extraction', 403)
              return json({ version: 1, tasks: extractions.list() })
            }
            if (request.method === 'DELETE') {
              if (!options.managementEnabled)
                return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable archive extraction', 403)
              const invalidOrigin = validateMutationOrigin(request, options.address)
              if (invalidOrigin)
                return invalidOrigin
              if (url.searchParams.get('terminal') !== '1')
                return error('INVALID_EXTRACTION', 'Use terminal=1 to clear completed extractions', 400)
              extractions.clearTerminal()
              return new Response(null, { status: 204, headers: API_HEADERS })
            }
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable archive extraction', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json')
              return error('UNSUPPORTED_MEDIA_TYPE', 'Extraction requests must use JSON', 415)
            let body: unknown
            try {
              body = await request.json()
            }
            catch {
              return error('INVALID_EXTRACTION', 'Extraction request must be valid JSON', 400)
            }
            const parsed = extractionRequestSchema.safeParse(body)
            if (!parsed.success)
              return error('INVALID_EXTRACTION', 'Extraction request is invalid', 400)
            try {
              const tasks = await extractions.enqueue(parsed.data.paths)
              return json({ version: 1, tasks }, 202)
            }
            catch (cause) {
              return extractionError(cause)
            }
          }
          const extractionAction = /^\/api\/extractions\/([^/]+)\/(cancel|retry)$/.exec(url.pathname)
          if (extractionAction) {
            const invalidMethod = requireMethod(request, 'POST')
            if (invalidMethod)
              return invalidMethod
            if (!options.managementEnabled)
              return error('MANAGEMENT_DISABLED', 'Start fs with --manage to enable archive extraction', 403)
            const invalidOrigin = validateMutationOrigin(request, options.address)
            if (invalidOrigin)
              return invalidOrigin
            let taskId: string
            try {
              taskId = decodeURIComponent(extractionAction[1]!)
            }
            catch {
              return error('INVALID_EXTRACTION', 'Extraction task ID is invalid', 400)
            }
            try {
              const task = extractionAction[2] === 'cancel'
                ? extractions.cancel(taskId)
                : await extractions.retry(taskId)
              if (!task)
                return extractionError(new ExtractionError('EXTRACTION_NOT_FOUND', 'Extraction task was not found'))
              return json({ version: 1, task }, extractionAction[2] === 'retry' ? 202 : 200)
            }
            catch (cause) {
              return extractionError(cause)
            }
          }
          if (url.pathname === '/files' || url.pathname.startsWith('/files/'))
            return streamOriginalFile(request, options.workspace, !options.authentication, options.safeHtml === true)
          if (url.pathname.startsWith('/thumbnails/'))
            return streamThumbnail(request, thumbnails)
          return error('NOT_FOUND', 'Route not found', 404)
        })()
      }
      catch (cause) {
        if (!(cause instanceof FileSessionError))
          throw cause
        response = error('SESSION_UNAVAILABLE', 'Session storage is unavailable', 503)
      }
      if (refreshedSession && !response.headers.has('Set-Cookie'))
        response.headers.set('Set-Cookie', activeSessionCookie(token!, refreshedSession.expiresAt))
      return response
    },
  })

  return {
    url: new URL(server.url),
    finished,
    async stop() {
      options.authentication?.close()
      await chunkedUploads?.close()
      await downloads.close()
      await extractions.close()
      thumbnails.close()
      await server.stop(true)
      finish?.()
    },
  }
}
