import type { AgentHelloMessage, AgentWelcomeMessage, DesiredStateMessage, FrpProcessState, ServerToAgentMessage, StructuredRuntimeError } from '../types'
import type { ClientReconciler } from './reconciler'
import process from 'node:process'
import { getLogger } from '../../../shared/log'
import { backoffDelay, DEFAULT_BACKOFF_MS } from '../backoff'
import { FRP_VERSION, resolveFrpArtifact } from '../frp/manifest'
import { TUNNEL_PROTOCOL_VERSION, TunnelError } from '../types'

export interface ClientAgentOptions {
  server: URL
  token: string
  ycyVersion: string
  lastAppliedRevision: number
  createReconciler: (welcome: AgentWelcomeMessage) => Promise<ClientReconciler>
  createWebSocket?: (url: URL, token: string) => WebSocket
  fetch?: (input: string | URL | Request, init?: RequestInit) => Promise<Response>
  backoffMs?: readonly number[]
  onAuthenticated?: () => Promise<void> | void
}

function websocketUrl(server: URL): URL {
  const url = new URL('/api/agent', server)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url
}

function parseServerMessage(value: unknown): ServerToAgentMessage | undefined {
  try {
    const raw = typeof value === 'string' ? value : new TextDecoder().decode(value as ArrayBuffer)
    return JSON.parse(raw) as ServerToAgentMessage
  }
  catch {
    return undefined
  }
}

function sleep(milliseconds: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, milliseconds))
}

export class TunnelClientAgent {
  private socket?: WebSocket
  private reconciler?: ClientReconciler
  private welcome?: AgentWelcomeMessage
  private stopped = false
  private revoked = false
  private fatalError?: Error
  private authenticationFailure?: Promise<void>
  private reconcilerStop?: Promise<void>
  private messageQueue = Promise.resolve()
  private authenticationReported = false
  private processState: { state: FrpProcessState, error?: StructuredRuntimeError } = { state: 'stopped' }
  private readonly backoffMs: readonly number[]
  private readonly logger = getLogger('tunnel.client.agent')

  constructor(private readonly options: ClientAgentOptions) {
    this.backoffMs = options.backoffMs ?? DEFAULT_BACKOFF_MS
  }

  async run(): Promise<void> {
    let failures = 0
    while (!this.stopped && !this.revoked && !this.fatalError) {
      if (await this.tokenAccepted() === false) {
        await this.failAuthentication('Client Token was rejected by the Tunnel Control Plane')
        break
      }
      const welcomed = await this.connectOnce()
      if (welcomed)
        failures = 0
      if (this.stopped || this.revoked || this.fatalError)
        break
      const delay = backoffDelay(failures, this.backoffMs)
      failures++
      this.logger.debug('Scheduling tunnel control reconnect', { delayMs: delay, attempt: failures })
      await sleep(delay)
    }
    if (this.fatalError)
      throw this.fatalError
  }

  private async tokenAccepted(): Promise<boolean | undefined> {
    try {
      const response = await (this.options.fetch ?? globalThis.fetch)(new URL('/api/agent', this.options.server), {
        headers: { Authorization: `Bearer ${this.options.token}` },
      })
      return ![401, 403].includes(response.status)
    }
    catch (cause) {
      this.logger.debug('Could not probe tunnel control plane authentication', { reason: cause instanceof Error ? cause.message : String(cause) })
      return undefined
    }
  }

  private failAuthentication(message: string): Promise<void> {
    if (!this.fatalError)
      this.logger.error('Tunnel client authentication failed', new TunnelError('AUTHENTICATION_FAILED', message))
    this.fatalError ??= new TunnelError('AUTHENTICATION_FAILED', message)
    this.authenticationFailure ??= this.stopReconciler() ?? Promise.resolve()
    return this.authenticationFailure
  }

  private stopReconciler(): Promise<void> | undefined {
    if (!this.reconciler)
      return undefined
    this.reconcilerStop ??= this.reconciler.stop()
    return this.reconcilerStop
  }

  private connectOnce(): Promise<boolean> {
    return new Promise((resolve) => {
      let welcomed = false
      let settled = false
      const BunWebSocket = WebSocket as unknown as { new (url: string | URL, options: Bun.WebSocketOptions): WebSocket }
      const socket = this.options.createWebSocket
        ? this.options.createWebSocket(websocketUrl(this.options.server), this.options.token)
        : new BunWebSocket(websocketUrl(this.options.server), { headers: { Authorization: `Bearer ${this.options.token}` } })
      this.socket = socket
      this.logger.debug('Connecting to tunnel control plane', { server: this.options.server.origin })
      const finish = (): void => {
        if (settled)
          return
        settled = true
        if (this.socket === socket)
          this.socket = undefined
        resolve(welcomed)
      }
      socket.addEventListener('open', () => {
        this.logger.debug('Tunnel control connection opened')
        const hello: AgentHelloMessage = {
          type: 'hello',
          tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
          ycyVersion: this.options.ycyVersion,
          platform: process.platform,
          architecture: process.arch,
          lastAppliedRevision: this.options.lastAppliedRevision,
        }
        socket.send(JSON.stringify(hello))
      })
      socket.addEventListener('message', (event) => {
        const message = parseServerMessage(event.data)
        if (!message) {
          socket.close(4400, 'Invalid control message')
          return
        }
        let preparedStop: Promise<void> | undefined
        if (message.type === 'revoke') {
          this.revoked = true
          preparedStop = this.stopReconciler()
        }
        const handled = this.messageQueue.then(async () => {
          await this.handleMessage(message, preparedStop)
          if (message.type === 'welcome')
            welcomed = true
        })
        this.messageQueue = handled.catch((cause) => {
          this.fatalError = cause instanceof Error ? cause : new Error(String(cause))
          socket.close(4406, 'Client failed to process control message')
        })
      })
      socket.addEventListener('error', (event) => {
        const message = 'message' in event && typeof event.message === 'string' ? event.message : ''
        if (/\b(?:401|403)\b/.test(message))
          void this.failAuthentication('Client Token was rejected by the Tunnel Control Plane')
        else
          this.logger.warn('Tunnel control connection error', { reason: message || 'unknown error' })
      })
      socket.addEventListener('close', (event) => {
        this.logger.debug('Tunnel control connection closed', { code: event.code, reason: event.reason || undefined })
        let closing = Promise.resolve()
        if ([4401, 4403].includes(event.code) && !this.revoked && !this.stopped) {
          closing = this.failAuthentication(event.reason || 'Client Token was rejected by the Tunnel Control Plane')
        }
        else if (event.code === 4406) {
          this.fatalError ??= new TunnelError('INCOMPATIBLE_CLIENT', event.reason || 'Tunnel client is incompatible with the control plane')
          closing = this.stopReconciler() ?? Promise.resolve()
        }
        const pendingMessages = this.messageQueue
        void Promise.allSettled([pendingMessages, closing]).then(finish)
      })
    })
  }

  private validateWelcome(message: AgentWelcomeMessage): void {
    const expected = resolveFrpArtifact()
    if (message.tunnelProtocolVersion !== TUNNEL_PROTOCOL_VERSION
      || message.requiredFrpVersion !== FRP_VERSION
      || message.artifact.version !== expected.version
      || message.artifact.archive !== expected.archive
      || message.artifact.url !== expected.url
      || message.artifact.sha256 !== expected.sha256
      || message.artifact.frpcSha256 !== expected.frpcSha256) {
      throw new TunnelError('INCOMPATIBLE_CLIENT', `Control plane requires an unsupported tunnel protocol or FRP build; upgrade ycy`)
    }
  }

  private async handleMessage(message: ServerToAgentMessage, preparedStop?: Promise<void>): Promise<void> {
    if (message.type === 'incompatible')
      throw new TunnelError('INCOMPATIBLE_CLIENT', message.message)
    if (message.tunnelProtocolVersion !== TUNNEL_PROTOCOL_VERSION)
      throw new TunnelError('INCOMPATIBLE_CLIENT', 'Control plane uses an unsupported tunnel protocol; upgrade ycy')
    if (message.type === 'welcome') {
      this.validateWelcome(message)
      if (!this.authenticationReported) {
        this.authenticationReported = true
        this.logger.info('Authenticated with tunnel control plane', { revision: message.snapshot.revision })
        await this.options.onAuthenticated?.()
      }
      this.welcome = message
      this.reconciler ??= await this.options.createReconciler(message)
      if (this.revoked || this.stopped) {
        await this.stopReconciler()
        return
      }
      await this.apply(message)
      return
    }
    if (message.type === 'desired_state') {
      if (!this.welcome || !this.reconciler)
        throw new TunnelError('INVALID_PROTOCOL', 'Desired state arrived before the agent handshake')
      await this.apply(message)
      return
    }
    if (message.type === 'restart_frpc') {
      await this.reconciler?.restart()
      return
    }
    if (message.type === 'revoke') {
      this.revoked = true
      await (preparedStop ?? this.stopReconciler())
      this.logger.warn(`Client Token ${message.reason === 'rotated' ? 'was rotated' : 'was deleted'}`)
      this.socket?.close(1000, 'Revoked')
    }
  }

  private async apply(message: AgentWelcomeMessage | DesiredStateMessage): Promise<void> {
    const context = message.type === 'welcome' ? message : this.welcome!
    try {
      await this.reconciler!.apply({
        advertisedFrpHost: context.advertisedFrpHost,
        advertisedFrpPort: context.advertisedFrpPort,
        internalFrpToken: context.internalFrpToken,
        snapshot: message.snapshot,
      })
      this.options.lastAppliedRevision = Math.max(this.options.lastAppliedRevision, message.snapshot.revision)
      this.send({
        type: 'apply_result',
        tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
        revision: message.snapshot.revision,
        success: true,
      })
      this.publishProcessState()
    }
    catch (cause) {
      if (this.revoked || this.stopped)
        return
      const error: StructuredRuntimeError = {
        code: cause instanceof TunnelError ? cause.code : 'APPLY_FAILED',
        message: cause instanceof Error ? cause.message : String(cause),
        revision: message.snapshot.revision,
      }
      this.logger.error('Could not apply desired tunnel state', cause, { revision: message.snapshot.revision })
      this.send({ type: 'apply_result', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, revision: message.snapshot.revision, success: false, error })
      this.publishProcessState()
    }
  }

  reportProcessState(state: FrpProcessState, error?: StructuredRuntimeError): void {
    this.processState = { state, ...(error ? { error } : {}) }
    this.publishProcessState()
  }

  private publishProcessState(): void {
    this.send({ type: 'process_state', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, ...this.processState })
  }

  private send(message: object): void {
    if (this.socket?.readyState === WebSocket.OPEN)
      this.socket.send(JSON.stringify(message))
  }

  async stop(): Promise<void> {
    this.stopped = true
    this.socket?.close(1000, 'Client stopping')
    await this.stopReconciler()
  }
}
