import type { ServerWebSocket } from 'bun'
import type { Buffer } from 'node:buffer'
import type { AgentHelloMessage, AgentToServerMessage, ClientRuntimeState, FrpProcessState, ServerToAgentMessage } from '../types'
import type { ControlPlaneEvent, TunnelControlPlane } from './control-plane'
import { getLogger } from '../../../shared/log'
import { FRP_VERSION, resolveFrpArtifact } from '../frp/manifest'
import { TUNNEL_PROTOCOL_VERSION, TunnelError } from '../types'

export interface AgentSocketData {
  clientId: string
  requestHost: string
  phase: 'awaiting_hello' | 'active'
  awaitingPong: boolean
}

interface RuntimeEntry extends ClientRuntimeState {
  socket?: ServerWebSocket<AgentSocketData>
}

export interface AgentAuthorization {
  data: AgentSocketData
}

export interface FrpsAvailability {
  state: () => { state: FrpProcessState }
  observe: (listener: (state: { state: FrpProcessState }) => void) => () => void
}

function parseBearer(request: Request): string | undefined {
  const authorization = request.headers.get('Authorization')
  if (!authorization)
    return undefined
  const separator = authorization.indexOf(' ')
  if (separator < 1 || authorization.slice(0, separator).toLowerCase() !== 'bearer')
    return undefined
  const value = authorization.slice(separator + 1).trim()
  return value || undefined
}

function parseMessage(message: string | Buffer): AgentToServerMessage | undefined {
  try {
    return JSON.parse(typeof message === 'string' ? message : message.toString('utf8')) as AgentToServerMessage
  }
  catch {
    return undefined
  }
}

function isHello(message: AgentToServerMessage): message is AgentHelloMessage {
  return message.type === 'hello'
    && Number.isSafeInteger(message.tunnelProtocolVersion)
    && typeof message.ycyVersion === 'string'
    && typeof message.platform === 'string'
    && typeof message.architecture === 'string'
    && Number.isSafeInteger(message.lastAppliedRevision)
    && message.lastAppliedRevision >= 0
}

export class AgentGateway {
  private readonly runtime = new Map<string, RuntimeEntry>()
  private readonly pendingConnections = new Set<string>()
  private readonly listeners = new Set<(clientId?: string) => void>()
  private readonly unsubscribe: () => void
  private readonly unsubscribeFrps: () => void
  private readonly livenessTimer: ReturnType<typeof setInterval>
  private readonly logger = getLogger('tunnel.server.gateway')
  private frpsState: FrpProcessState

  constructor(
    private readonly controlPlane: TunnelControlPlane,
    private readonly frpPort: number,
    private readonly frpToken: string,
    private readonly advertisedAddress?: { host: string, port: number },
    frps?: FrpsAvailability,
  ) {
    this.unsubscribe = controlPlane.subscribe(event => this.handleControlPlaneEvent(event))
    this.frpsState = frps?.state().state ?? 'running'
    this.unsubscribeFrps = frps?.observe(state => this.setFrpsState(state.state)) ?? (() => {})
    this.livenessTimer = setInterval(() => this.checkLiveness(), 30_000)
  }

  observe(listener: (clientId?: string) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private changed(clientId?: string): void {
    for (const listener of this.listeners)
      listener(clientId)
  }

  state(clientId: string): ClientRuntimeState {
    const current = this.runtime.get(clientId)
    if (current)
      return { connectionState: current.connectionState, processState: current.processState, ...(current.lastError ? { lastError: current.lastError } : {}) }
    return { connectionState: this.revocationPending(clientId) ? 'revocation_pending' : 'disconnected', processState: 'stopped' }
  }

  private revocationPending(clientId: string): boolean {
    try {
      return this.controlPlane.getClient(clientId).revocationPending
    }
    catch (cause) {
      if (cause instanceof TunnelError && cause.code === 'NOT_FOUND')
        return false
      throw cause
    }
  }

  authorize(request: Request): AgentAuthorization | Response {
    if (this.frpsState !== 'running')
      return Response.json({ version: 1, error: { code: 'FRPS_UNAVAILABLE', message: 'Managed frps is not running' } }, { status: 503 })
    const token = parseBearer(request)
    const client = token ? this.controlPlane.findClientByToken(token) : undefined
    if (!client)
      return Response.json({ version: 1, error: { code: 'AUTHENTICATION_FAILED', message: 'Client Token is invalid' } }, { status: 401 })
    if (this.runtime.get(client.id)?.socket || this.pendingConnections.has(client.id)) {
      this.logger.debug('Rejected duplicate tunnel client authorization', { clientId: client.id })
      return Response.json({ version: 1, error: { code: 'CLIENT_CONNECTED', message: 'Client Token already has an active control session' } }, { status: 409 })
    }
    this.pendingConnections.add(client.id)
    return {
      data: {
        clientId: client.id,
        requestHost: new URL(request.url).hostname,
        phase: 'awaiting_hello',
        awaitingPong: false,
      },
    }
  }

  open(socket: ServerWebSocket<AgentSocketData>): void {
    this.pendingConnections.delete(socket.data.clientId)
    if (this.frpsState !== 'running') {
      socket.close(4503, 'Managed frps is not running')
      return
    }
    const existing = this.runtime.get(socket.data.clientId)
    if (existing?.socket) {
      socket.close(4409, 'Client Token already connected')
      return
    }
    this.controlPlane.acknowledgeReplacementToken(socket.data.clientId)
    this.runtime.set(socket.data.clientId, { connectionState: 'connected', processState: existing?.processState ?? 'stopped', socket })
    this.logger.info('Trusted tunnel client connected', { clientId: socket.data.clientId })
    this.changed(socket.data.clientId)
  }

  cancelAuthorization(clientId: string): void {
    this.pendingConnections.delete(clientId)
  }

  message(socket: ServerWebSocket<AgentSocketData>, raw: string | Buffer): void {
    const message = parseMessage(raw)
    if (!message) {
      this.logger.warn('Rejected invalid client control message', { clientId: socket.data.clientId })
      socket.close(4400, 'Invalid JSON message')
      return
    }
    if (socket.data.phase === 'awaiting_hello') {
      if (!isHello(message)) {
        socket.close(4400, 'A valid hello message is required')
        return
      }
      this.handshake(socket, message)
      return
    }
    if (message.tunnelProtocolVersion !== TUNNEL_PROTOCOL_VERSION) {
      socket.close(4406, 'Unsupported tunnel protocol version')
      return
    }
    if (message.type === 'apply_result') {
      if (!Number.isSafeInteger(message.revision) || message.revision < 0 || typeof message.success !== 'boolean') {
        socket.close(4400, 'Invalid apply result')
        return
      }
      if (message.success) {
        try {
          this.controlPlane.recordAppliedRevision(socket.data.clientId, message.revision)
          this.updateRuntime(socket.data.clientId, { lastError: undefined })
        }
        catch {
          socket.close(4400, 'Invalid Applied Revision')
        }
      }
      else {
        this.logger.warn('Trusted tunnel client failed to apply desired state', { clientId: socket.data.clientId, revision: message.revision, code: message.error?.code ?? 'APPLY_FAILED' })
        this.updateRuntime(socket.data.clientId, {
          lastError: message.error ?? { code: 'APPLY_FAILED', message: 'Client could not apply Desired Revision', revision: message.revision },
        })
      }
      return
    }
    if (message.type === 'process_state') {
      if (!['stopped', 'running', 'recovering', 'configuration_failed'].includes(message.state)) {
        socket.close(4400, 'Invalid process state')
        return
      }
      const previousError = this.runtime.get(socket.data.clientId)?.lastError
      const lastError = message.error ?? (previousError?.revision === undefined ? undefined : previousError)
      this.updateRuntime(socket.data.clientId, { processState: message.state, lastError })
      return
    }
    socket.close(4400, 'Unexpected agent message')
  }

  private handshake(socket: ServerWebSocket<AgentSocketData>, hello: AgentHelloMessage): void {
    if (this.frpsState !== 'running') {
      socket.close(4503, 'Managed frps is not running')
      return
    }
    if (hello.tunnelProtocolVersion !== TUNNEL_PROTOCOL_VERSION) {
      this.incompatible(socket, `Client tunnel protocol ${hello.tunnelProtocolVersion} is incompatible; upgrade ycy`)
      return
    }
    let artifact
    try {
      artifact = resolveFrpArtifact(hello.platform as NodeJS.Platform, hello.architecture as NodeJS.Architecture)
    }
    catch {
      this.incompatible(socket, `FRP ${FRP_VERSION} is unavailable for ${hello.platform}/${hello.architecture}; upgrade ycy or use a supported platform`)
      return
    }
    if (hello.lastAppliedRevision > this.controlPlane.getClient(socket.data.clientId).desiredRevision) {
      this.incompatible(socket, 'Client Applied Revision exceeds the control plane Desired Revision; inspect or upgrade the client')
      return
    }
    socket.data.phase = 'active'
    const advertised = this.advertisedAddress ?? { host: socket.data.requestHost, port: this.frpPort }
    const welcome: ServerToAgentMessage = {
      type: 'welcome',
      tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
      requiredFrpVersion: FRP_VERSION,
      artifact: {
        version: artifact.version,
        archive: artifact.archive,
        url: artifact.url,
        sha256: artifact.sha256,
        frpcSha256: artifact.frpcSha256,
      },
      advertisedFrpHost: advertised.host,
      advertisedFrpPort: advertised.port,
      internalFrpToken: this.frpToken,
      snapshot: this.controlPlane.snapshot(socket.data.clientId),
    }
    socket.send(JSON.stringify(welcome))
    this.logger.info('Trusted tunnel client authenticated', { clientId: socket.data.clientId, revision: welcome.snapshot.revision })
  }

  private incompatible(socket: ServerWebSocket<AgentSocketData>, message: string): void {
    const payload: ServerToAgentMessage = { type: 'incompatible', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, message }
    socket.send(JSON.stringify(payload))
    this.updateRuntime(socket.data.clientId, { connectionState: 'incompatible' })
    this.logger.warn('Rejected incompatible trusted tunnel client', { clientId: socket.data.clientId, reason: message })
    socket.close(4406, 'Incompatible client')
  }

  close(socket: ServerWebSocket<AgentSocketData>): void {
    const current = this.runtime.get(socket.data.clientId)
    if (current?.socket !== socket)
      return
    const clientState = this.state(socket.data.clientId)
    this.runtime.set(socket.data.clientId, {
      ...current,
      socket: undefined,
      connectionState: this.revocationPending(socket.data.clientId) ? 'revocation_pending' : clientState.connectionState === 'incompatible' ? 'incompatible' : 'disconnected',
    })
    this.logger.info('Trusted tunnel client disconnected', { clientId: socket.data.clientId })
    this.changed(socket.data.clientId)
  }

  pong(socket: ServerWebSocket<AgentSocketData>): void {
    socket.data.awaitingPong = false
  }

  private checkLiveness(): void {
    for (const entry of this.runtime.values()) {
      if (!entry.socket)
        continue
      if (entry.socket.data.awaitingPong) {
        entry.socket.close(4408, 'Control connection timed out')
        continue
      }
      entry.socket.data.awaitingPong = true
      entry.socket.ping()
    }
  }

  private updateRuntime(clientId: string, patch: Partial<RuntimeEntry>): void {
    const current = this.runtime.get(clientId) ?? { connectionState: 'disconnected', processState: 'stopped' }
    this.runtime.set(clientId, { ...current, ...patch })
    this.changed(clientId)
  }

  private setFrpsState(state: FrpProcessState): void {
    const previous = this.frpsState
    const wasRunning = this.frpsState === 'running'
    this.frpsState = state
    if (previous !== state)
      this.logger.info('Managed frps state changed', { state })
    if (wasRunning && state !== 'running') {
      for (const entry of this.runtime.values())
        entry.socket?.close(4503, 'Managed frps is not running')
      this.pendingConnections.clear()
    }
    this.changed()
  }

  private send(clientId: string, message: ServerToAgentMessage): boolean {
    const socket = this.runtime.get(clientId)?.socket
    if (!socket || socket.data.phase !== 'active')
      return false
    socket.send(JSON.stringify(message))
    return true
  }

  private handleControlPlaneEvent(event: ControlPlaneEvent): void {
    if (event.type === 'desired_state') {
      this.send(event.clientId, {
        type: 'desired_state',
        tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
        snapshot: this.controlPlane.snapshot(event.clientId),
      })
    }
    else if (event.type === 'client_rotated' || event.type === 'client_deleted') {
      const entry = this.runtime.get(event.clientId)
      if (entry?.socket) {
        entry.socket.send(JSON.stringify({
          type: 'revoke',
          tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
          reason: event.type === 'client_rotated' ? 'rotated' : 'deleted',
        } satisfies ServerToAgentMessage))
        entry.socket.close(4401, 'Client Token revoked')
      }
      if (event.type === 'client_deleted') {
        this.runtime.delete(event.clientId)
        this.pendingConnections.delete(event.clientId)
      }
      else {
        this.updateRuntime(event.clientId, { connectionState: 'revocation_pending' })
      }
    }
  }

  restartClient(clientId: string): boolean {
    this.controlPlane.getClient(clientId)
    return this.send(clientId, { type: 'restart_frpc', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION })
  }

  stop(): void {
    clearInterval(this.livenessTimer)
    this.unsubscribe()
    this.unsubscribeFrps()
    this.pendingConnections.clear()
    for (const entry of this.runtime.values())
      entry.socket?.close(1001, 'Tunnel server stopping')
    this.runtime.clear()
    this.changed()
  }
}
