import type { TunnelErrorCode } from '../types'
import type { AgentGateway, AgentSocketData } from './agent-gateway'
import type { TunnelMutationInput, TunnelPatchInput } from './control-plane'
import type { TunnelManagement, TunnelWorkspace } from './tunnel-management'
import { z } from 'zod'
import { getLogger } from '../../../shared/log'
import { TunnelError } from '../types'

const API_HEADERS = {
  'Cache-Control': 'no-store',
  'Content-Security-Policy': 'default-src \'none\'; frame-ancestors \'none\'',
  'Referrer-Policy': 'no-referrer',
  'X-Content-Type-Options': 'nosniff',
}

const SESSION_COOKIE = 'ycy_tunnel_session'
const SESSION_COOKIE_ATTRIBUTES = 'HttpOnly; SameSite=Strict; Path=/'
const sessionSchema = z.object({ username: z.string(), password: z.string() }).strict()
const passwordChangeSchema = z.object({ currentPassword: z.string(), newPassword: z.string() }).strict()
const accountCreateSchema = z.object({ username: z.string(), password: z.string(), role: z.enum(['admin', 'user']).optional() }).strict()
const accountRoleSchema = z.object({ role: z.enum(['admin', 'user']) }).strict()
const accountPasswordSchema = z.object({ password: z.string() }).strict()
const clientCreateSchema = z.object({ remark: z.string().optional() }).strict()
const clientRemarkSchema = z.object({ remark: z.string() }).strict()
const custom404PageSchema = z.object({ content: z.string() }).strict()
const tunnelImportPreviewSchema = z.object({ source: z.string().min(1).max(1024 * 1024) }).strict()
const tunnelImportSchema = tunnelImportPreviewSchema.extend({ candidateIds: z.array(z.string()).min(1) }).strict()
const tunnelHeaderSchema = z.object({ name: z.string(), value: z.string() }).strict()
const tunnelTransportSchema = z.object({
  useEncryption: z.boolean().optional(),
  useCompression: z.boolean().optional(),
  bandwidthLimit: z.object({ value: z.number(), unit: z.enum(['KB', 'MB']), mode: z.enum(['client', 'server']) }).strict().nullable().optional(),
  proxyProtocolVersion: z.enum(['v1', 'v2']).nullable().optional(),
}).strict()
const tunnelHealthCheckSchema = z.discriminatedUnion('type', [
  z.object({ type: z.literal('tcp'), intervalSeconds: z.number().int(), timeoutSeconds: z.number().int(), maxFailed: z.number().int() }).strict(),
  z.object({ type: z.literal('http'), path: z.string(), intervalSeconds: z.number().int(), timeoutSeconds: z.number().int(), maxFailed: z.number().int(), headers: z.array(tunnelHeaderSchema).optional() }).strict(),
])
const tunnelHttpOptionsSchema = z.object({
  basicAuth: z.object({ username: z.string(), password: z.string().optional() }).strict().nullable().optional(),
  hostHeaderRewrite: z.string().nullable().optional(),
  requestHeaders: z.array(tunnelHeaderSchema).optional(),
  responseHeaders: z.array(tunnelHeaderSchema).optional(),
}).strict()
const tunnelOptionsSchema = z.object({
  transport: tunnelTransportSchema.optional(),
  healthCheck: tunnelHealthCheckSchema.nullable().optional(),
  http: tunnelHttpOptionsSchema.nullable().optional(),
}).strict()
const tunnelSchema = z.object({
  label: z.string().optional(),
  protocol: z.enum(['http', 'tcp', 'udp']),
  customDomains: z.array(z.string()).optional(),
  hostname: z.string().nullable().optional(),
  location: z.string().nullable().optional(),
  serverPort: z.number().int().nullable().optional(),
  localHost: z.string().optional(),
  localPort: z.number().int(),
  enabled: z.boolean().optional(),
  options: tunnelOptionsSchema.optional(),
}).strict()
const tunnelPatchSchema = tunnelSchema.partial()

const TUNNEL_ERROR_STATUS = {
  ACCOUNT_NOT_EMPTY: 409,
  ACTIVATION_FAILED: 500,
  AUTHENTICATION_FAILED: 401,
  AUTHENTICATION_REQUIRED: 401,
  CLIENT_OFFLINE: 409,
  CLIENT_STOPPED: 409,
  CONFIGURATION_FAILED: 500,
  FORBIDDEN: 403,
  FRP_INSTALL_FAILED: 500,
  FRPS_UNAVAILABLE: 503,
  INCOMPATIBLE_CLIENT: 409,
  INSTANCE_ACTIVE: 409,
  INVALID_ACCOUNT: 400,
  INVALID_CLIENT_REMARK: 400,
  INVALID_CUSTOM_404_PAGE: 400,
  INVALID_CONFIG: 400,
  INVALID_CURRENT_PASSWORD: 400,
  INVALID_FRP_ARCHIVE: 500,
  INVALID_FRP_BINARY: 500,
  INVALID_FRP_VERSION: 500,
  INVALID_HOSTNAME: 400,
  INVALID_HTTP_ROUTE: 400,
  INVALID_LOCAL_ENDPOINT: 400,
  INVALID_PROTOCOL: 400,
  INVALID_REVISION: 400,
  INVALID_TUNNEL: 400,
  LOCK_UNAVAILABLE: 500,
  MANAGED_ACCOUNT: 409,
  NOT_FOUND: 404,
  PORT_OUTSIDE_POOL: 400,
  PORT_POOL_EXHAUSTED: 409,
  RESOURCE_RESERVED: 409,
  SESSION_UNAVAILABLE: 503,
  UNSUPPORTED_PLATFORM: 500,
  USERNAME_TAKEN: 409,
} as const satisfies Record<TunnelErrorCode, number>

function json(data: unknown, status = 200, headers?: HeadersInit): Response {
  return Response.json(data, { status, headers: { ...API_HEADERS, ...headers } })
}

function error(code: string, message: string, status: number): Response {
  return json({ version: 1, error: { code, message } }, status)
}

function errorResponse(cause: unknown, request?: Request): Response {
  if (cause instanceof TunnelError)
    return error(cause.code, cause.message, TUNNEL_ERROR_STATUS[cause.code])
  getLogger('tunnel.server.http').error('Tunnel control request failed', cause, request ? { method: request.method, path: new URL(request.url).pathname } : undefined)
  return error('INTERNAL_ERROR', 'The tunnel control request failed', 500)
}

async function requestJson<T>(request: Request, schema: z.ZodType<T>): Promise<T | Response> {
  if (request.headers.get('Content-Type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json')
    return error('UNSUPPORTED_MEDIA_TYPE', 'Request body must use JSON', 415)
  try {
    const parsed = schema.safeParse(await request.json())
    return parsed.success ? parsed.data : error('INVALID_REQUEST', 'Request body is invalid', 400)
  }
  catch {
    return error('INVALID_REQUEST', 'Request body must be valid JSON', 400)
  }
}

function sameOrigin(request: Request): boolean {
  const origin = request.headers.get('Origin')
  if (!origin)
    return true
  try {
    return new URL(origin).host === new URL(request.url).host
  }
  catch {
    return false
  }
}

function parseId(pathname: string, pattern: RegExp): string | undefined {
  const value = pattern.exec(pathname)?.[1]
  if (!value)
    return undefined
  try {
    return decodeURIComponent(value)
  }
  catch {
    return undefined
  }
}

function cookie(request: Request): string | undefined {
  return request.headers.get('Cookie')?.split(';').map(value => value.trim()).find(value => value.startsWith(`${SESSION_COOKIE}=`))?.slice(SESSION_COOKIE.length + 1)
}

function sessionCookie(token: string, expiresAt: string): string {
  const maxAge = Math.max(1, Math.ceil((Date.parse(expiresAt) - Date.now()) / 1000))
  return `${SESSION_COOKIE}=${token}; ${SESSION_COOKIE_ATTRIBUTES}; Max-Age=${maxAge}`
}

function expiredSessionCookie(): string {
  return `${SESSION_COOKIE}=; ${SESSION_COOKIE_ATTRIBUTES}; Max-Age=0`
}

export interface TunnelControlApiOptions {
  management: TunnelManagement
  gateway: AgentGateway
}

export class TunnelControlApi {
  constructor(private readonly options: TunnelControlApiOptions) {}

  private workspace(request: Request): TunnelWorkspace | Response {
    return this.options.management.resume(cookie(request))
      ?? error('AUTHENTICATION_REQUIRED', 'Authenticated session is required', 401)
  }

  async handle(request: Request, server: Bun.Server<AgentSocketData>): Promise<Response | undefined> {
    const url = new URL(request.url)
    if (url.pathname === '/healthz')
      return request.method === 'GET' ? json({ status: 'ok' }) : error('METHOD_NOT_ALLOWED', 'Use GET', 405)

    if (url.pathname === '/api/agent') {
      if (request.method !== 'GET')
        return error('METHOD_NOT_ALLOWED', 'Use GET with WebSocket upgrade', 405)
      const authorization = this.options.gateway.authorize(request)
      if (authorization instanceof Response)
        return authorization
      if (server.upgrade(request, { data: authorization.data }))
        return undefined
      this.options.gateway.cancelAuthorization(authorization.data.clientId)
      return error('UPGRADE_REQUIRED', 'WebSocket upgrade is required', 426)
    }

    if (url.pathname === '/api/session' && request.method === 'POST') {
      if (!sameOrigin(request))
        return error('ORIGIN_FORBIDDEN', 'Mutation requests must be same-origin', 403)
      const body = await requestJson(request, sessionSchema)
      if (body instanceof Response)
        return body
      try {
        const grant = await this.options.management.signIn(body)
        return json({ version: 1, authenticated: true, account: grant.account }, 200, { 'Set-Cookie': sessionCookie(grant.token, grant.expiresAt) })
      }
      catch (cause) {
        return errorResponse(cause, request)
      }
    }

    if (!url.pathname.startsWith('/api/'))
      return error('NOT_FOUND', 'Route not found', 404)
    const token = cookie(request)
    let workspace: TunnelWorkspace | Response
    try {
      workspace = this.workspace(request)
    }
    catch (cause) {
      return errorResponse(cause, request)
    }
    if (workspace instanceof Response)
      return workspace
    if (!['GET', 'HEAD'].includes(request.method) && !sameOrigin(request))
      return error('ORIGIN_FORBIDDEN', 'Mutation requests must be same-origin', 403)

    const response = await (async (): Promise<Response> => {
      try {
        if (url.pathname === '/api/session' && request.method === 'GET')
          return json({ version: 1, authenticated: true, account: workspace.account })
        if (url.pathname === '/api/session' && request.method === 'DELETE') {
          this.options.management.signOut(token)
          return new Response(null, { status: 204, headers: { ...API_HEADERS, 'Set-Cookie': expiredSessionCookie() } })
        }
        if (url.pathname === '/api/session/password' && request.method === 'PUT') {
          const body = await requestJson(request, passwordChangeSchema)
          if (body instanceof Response)
            return body
          await workspace.changePassword(body)
          return new Response(null, { status: 204, headers: { ...API_HEADERS, 'Set-Cookie': expiredSessionCookie() } })
        }
        if (url.pathname === '/api/events' && request.method === 'GET') {
          server.timeout(request, 0)
          const encoder = new TextEncoder()
          let dispose: (() => void) | undefined
          const stream = new ReadableStream<Uint8Array>({
            start: (controller) => {
              const publish = (event: 'changed' | 'session_revoked'): void => {
                controller.enqueue(encoder.encode(`data: ${JSON.stringify({ version: 1, event })}\n\n`))
                if (event === 'session_revoked')
                  controller.close()
              }
              dispose = workspace.observe(publish)
              publish('changed')
            },
            cancel() {
              dispose?.()
            },
          })
          return new Response(stream, { headers: { ...API_HEADERS, 'Content-Type': 'text/event-stream; charset=utf-8', 'X-Accel-Buffering': 'no' } })
        }
        if (url.pathname === '/api/state' && request.method === 'GET')
          return json({ version: 1, ...await workspace.overview() })

        if (url.pathname === '/api/accounts') {
          const administration = workspace.administration()
          if (request.method === 'GET')
            return json({ version: 1, accounts: administration.listAccounts() })
          if (request.method === 'POST') {
            const body = await requestJson(request, accountCreateSchema)
            if (body instanceof Response)
              return body
            return json({ version: 1, account: await administration.createAccount(body) }, 201)
          }
          return error('METHOD_NOT_ALLOWED', 'Use GET or POST', 405)
        }
        const accountId = parseId(url.pathname, /^\/api\/accounts\/([^/]+)$/)
        if (accountId) {
          const administration = workspace.administration()
          if (request.method === 'PATCH') {
            const body = await requestJson(request, accountRoleSchema)
            if (body instanceof Response)
              return body
            const self = accountId === workspace.account.id
            const account = administration.changeAccountRole(accountId, body.role)
            return json({ version: 1, account }, 200, self ? { 'Set-Cookie': expiredSessionCookie() } : undefined)
          }
          if (request.method === 'DELETE') {
            const self = accountId === workspace.account.id
            administration.deleteAccount(accountId)
            return new Response(null, { status: 204, headers: { ...API_HEADERS, ...(self ? { 'Set-Cookie': expiredSessionCookie() } : {}) } })
          }
          return error('METHOD_NOT_ALLOWED', 'Use PATCH or DELETE', 405)
        }
        const accountPasswordId = parseId(url.pathname, /^\/api\/accounts\/([^/]+)\/password$/)
        if (accountPasswordId && request.method === 'PUT') {
          const body = await requestJson(request, accountPasswordSchema)
          if (body instanceof Response)
            return body
          const self = accountPasswordId === workspace.account.id
          await workspace.administration().resetAccountPassword(accountPasswordId, body.password)
          return new Response(null, { status: 204, headers: { ...API_HEADERS, ...(self ? { 'Set-Cookie': expiredSessionCookie() } : {}) } })
        }

        if (url.pathname === '/api/clients') {
          if (request.method === 'GET')
            return json({ version: 1, clients: workspace.listClients() })
          if (request.method === 'POST') {
            const body = await requestJson(request, clientCreateSchema)
            if (body instanceof Response)
              return body
            return json({ version: 1, client: workspace.createClient(body) }, 201)
          }
          return error('METHOD_NOT_ALLOWED', 'Use GET or POST', 405)
        }
        const clientId = parseId(url.pathname, /^\/api\/clients\/([^/]+)$/)
        if (clientId) {
          if (request.method === 'GET')
            return json({ version: 1, ...workspace.getClient(clientId) })
          if (request.method === 'PATCH') {
            const body = await requestJson(request, clientRemarkSchema)
            if (body instanceof Response)
              return body
            return json({ version: 1, client: workspace.updateClientRemark(clientId, body.remark) })
          }
          if (request.method === 'DELETE') {
            workspace.deleteClient(clientId)
            return new Response(null, { status: 204, headers: API_HEADERS })
          }
          return error('METHOD_NOT_ALLOWED', 'Use GET, PATCH, or DELETE', 405)
        }
        const rotateClientId = parseId(url.pathname, /^\/api\/clients\/([^/]+)\/rotate$/)
        if (rotateClientId && request.method === 'POST')
          return json({ version: 1, client: workspace.rotateClientToken(rotateClientId) })
        const restartClientId = parseId(url.pathname, /^\/api\/clients\/([^/]+)\/restart$/)
        if (restartClientId && request.method === 'POST') {
          workspace.restartClient(restartClientId)
          return json({ version: 1, accepted: true }, 202)
        }
        const tunnelImportPreviewClientId = parseId(url.pathname, /^\/api\/clients\/([^/]+)\/tunnels\/import\/preview$/)
        if (tunnelImportPreviewClientId) {
          if (request.method !== 'POST')
            return error('METHOD_NOT_ALLOWED', 'Use POST', 405)
          const body = await requestJson(request, tunnelImportPreviewSchema)
          if (body instanceof Response)
            return body
          return json({ version: 1, ...workspace.previewTunnelImport(tunnelImportPreviewClientId, body.source) })
        }
        const tunnelImportClientId = parseId(url.pathname, /^\/api\/clients\/([^/]+)\/tunnels\/import$/)
        if (tunnelImportClientId) {
          if (request.method !== 'POST')
            return error('METHOD_NOT_ALLOWED', 'Use POST', 405)
          const body = await requestJson(request, tunnelImportSchema)
          if (body instanceof Response)
            return body
          return json({ version: 1, tunnels: workspace.importTunnels(tunnelImportClientId, body.source, body.candidateIds) }, 201)
        }
        const tunnelsClientId = parseId(url.pathname, /^\/api\/clients\/([^/]+)\/tunnels$/)
        if (tunnelsClientId) {
          if (request.method === 'GET')
            return json({ version: 1, tunnels: workspace.getClient(tunnelsClientId).tunnels })
          if (request.method === 'POST') {
            const body = await requestJson(request, tunnelSchema)
            if (body instanceof Response)
              return body
            return json({ version: 1, tunnel: workspace.createTunnel(tunnelsClientId, body as TunnelMutationInput) }, 201)
          }
          return error('METHOD_NOT_ALLOWED', 'Use GET or POST', 405)
        }
        const tunnelId = parseId(url.pathname, /^\/api\/tunnels\/([^/]+)$/)
        if (tunnelId) {
          if (request.method === 'PATCH') {
            const body = await requestJson(request, tunnelPatchSchema)
            if (body instanceof Response)
              return body
            return json({ version: 1, tunnel: workspace.updateTunnel(tunnelId, body as TunnelPatchInput) })
          }
          if (request.method === 'DELETE') {
            workspace.deleteTunnel(tunnelId)
            return new Response(null, { status: 204, headers: API_HEADERS })
          }
          return error('METHOD_NOT_ALLOWED', 'Use PATCH or DELETE', 405)
        }
        if (url.pathname === '/api/server/frps/config/custom-404-page') {
          const configuration = workspace.administration().frpsConfiguration()
          if (request.method === 'GET')
            return json({ version: 1, ...await configuration.getCustom404Page() })
          if (request.method === 'PUT') {
            const body = await requestJson(request, custom404PageSchema)
            if (body instanceof Response)
              return body
            return json({ version: 1, ...await configuration.setCustom404Page(body.content) })
          }
          return error('METHOD_NOT_ALLOWED', 'Use GET or PUT', 405)
        }
        const action = /^\/api\/server\/frp\/(start|stop|restart)$/.exec(url.pathname)?.[1] as 'start' | 'stop' | 'restart' | undefined
        if (action && request.method === 'POST')
          return json({ version: 1, server: await workspace.administration().controlFrps(action) })
        return error('NOT_FOUND', 'Route not found', 404)
      }
      catch (cause) {
        return errorResponse(cause, request)
      }
    })()
    if (token && !response.headers.has('Set-Cookie')) {
      response.headers.set('Set-Cookie', sessionCookie(token, workspace.sessionExpiresAt))
    }
    return response
  }

  stop(): void {}
}
