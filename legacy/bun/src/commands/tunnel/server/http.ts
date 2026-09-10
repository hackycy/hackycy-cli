import type { AgentSocketData } from './agent-gateway'
import type { TunnelControlApiOptions } from './control-api'
import { TunnelControlApi } from './control-api'
import tunnelWebApp from './web/index.html'

export interface RunningTunnelHttpServer {
  readonly url: URL
  readonly finished: Promise<void>
  stop: () => Promise<void>
}

const APP_CONTENT_SECURITY_POLICY = 'default-src \'self\'; script-src \'self\'; style-src \'self\' \'unsafe-inline\'; img-src \'self\' data:; connect-src \'self\'; object-src \'none\'; base-uri \'none\'; frame-ancestors \'none\''

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

export interface TunnelHttpServerOptions extends TunnelControlApiOptions {
  address: string
  controlPort: number
}

export function startTunnelHttpServer(options: TunnelHttpServerOptions): RunningTunnelHttpServer {
  configureWebBundleHeaders(tunnelWebApp)
  const controlApi = new TunnelControlApi(options)
  let finish: (() => void) | undefined
  const finished = new Promise<void>(resolve => finish = resolve)
  const appRoute = { GET: tunnelWebApp as unknown as Response }
  let stopped = false
  const sockets = new Set<Bun.ServerWebSocket<AgentSocketData>>()

  const server = Bun.serve<AgentSocketData>({
    hostname: options.address,
    port: options.controlPort,
    routes: {
      '/': appRoute,
      '/clients': appRoute,
      '/clients/*': appRoute,
      '/server': appRoute,
      '/accounts': appRoute,
    },
    websocket: {
      open: (socket) => {
        sockets.add(socket)
        options.gateway.open(socket)
      },
      message: (socket, message) => options.gateway.message(socket, message),
      close: (socket) => {
        sockets.delete(socket)
        options.gateway.close(socket)
      },
      pong: socket => options.gateway.pong(socket),
      idleTimeout: 0,
      maxPayloadLength: 1024 * 1024,
    },
    fetch: (request, bunServer) => controlApi.handle(request, bunServer),
  })

  return {
    url: new URL(server.url),
    finished,
    async stop() {
      if (stopped)
        return
      stopped = true
      controlApi.stop()
      for (const socket of sockets)
        socket.terminate()
      sockets.clear()
      // Bun 1.3.14 stops listening but leaves this promise pending after any WebSocket upgrade.
      void server.stop(true)
      finish?.()
    },
  }
}
