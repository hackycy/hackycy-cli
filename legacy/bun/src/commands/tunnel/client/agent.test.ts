import type { AgentWelcomeMessage } from '../types'
import type { ClientReconciler, ReconcileInput } from './reconciler'
import { describe, expect, test } from 'bun:test'
import { FRP_VERSION, resolveFrpArtifact } from '../frp/manifest'
import { TUNNEL_PROTOCOL_VERSION } from '../types'
import { TunnelClientAgent } from './agent'

class FakeWebSocket extends EventTarget {
  static readonly OPEN = 1
  readyState = FakeWebSocket.OPEN
  sent: string[] = []

  send(value: string): void {
    this.sent.push(value)
  }

  close(code = 1000, reason = ''): void {
    if (this.readyState === 3)
      return
    this.readyState = 3
    queueMicrotask(() => this.dispatchEvent(new CloseEvent('close', { code, reason })))
  }

  open(): void {
    this.dispatchEvent(new Event('open'))
  }

  receive(value: unknown): void {
    this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(value) }))
  }
}

function welcome(overrides: Partial<AgentWelcomeMessage> = {}): AgentWelcomeMessage {
  const artifact = resolveFrpArtifact()
  return {
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
    advertisedFrpHost: 'frp.example.com',
    advertisedFrpPort: 7000,
    internalFrpToken: 'internal',
    snapshot: { clientKey: 'client', revision: 0, tunnels: [] },
    ...overrides,
  }
}

const acceptedTokenProbe = async (): Promise<Response> => new Response(null, { status: 426 })

describe('TunnelClientAgent', () => {
  test('authenticates before applying, acknowledges, and stops on revoke', async () => {
    const socket = new FakeWebSocket()
    const applied: ReconcileInput[] = []
    let stops = 0
    const reconciler = {
      apply: async (input: ReconcileInput) => applied.push(input),
      restart: async () => {},
      stop: async () => { stops++ },
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: acceptedTokenProbe,
      createWebSocket: () => socket as unknown as WebSocket,
      createReconciler: async () => reconciler,
      backoffMs: [1],
    })
    const running = agent.run()
    await Bun.sleep(0)
    socket.open()
    expect(JSON.parse(socket.sent[0]!)).toMatchObject({ type: 'hello', lastAppliedRevision: 0 })
    socket.receive(welcome())
    await Bun.sleep(1)
    expect(applied).toHaveLength(1)
    expect(socket.sent.map(value => JSON.parse(value))).toContainEqual(expect.objectContaining({ type: 'apply_result', success: true, revision: 0 }))
    socket.receive({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'rotated' })
    socket.close(4401, 'Client Token revoked')
    await running
    expect(stops).toBe(1)
  })

  test('refuses an unpinned artifact before creating the reconciler', async () => {
    const socket = new FakeWebSocket()
    let created = false
    let authenticated = 0
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: acceptedTokenProbe,
      createWebSocket: () => socket as unknown as WebSocket,
      createReconciler: async () => {
        created = true
        return {} as ClientReconciler
      },
      onAuthenticated: () => { authenticated++ },
      backoffMs: [1],
    })
    const running = agent.run()
    await Bun.sleep(0)
    socket.open()
    socket.receive(welcome({ artifact: { ...welcome().artifact, sha256: '0'.repeat(64) } }))
    await expect(running).rejects.toThrow('upgrade ycy')
    expect(created).toBe(false)
    expect(authenticated).toBe(0)
  })

  test('does not report authentication when the initial token probe is rejected', async () => {
    let authenticated = 0
    let sockets = 0
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'rejected-token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: async () => new Response(null, { status: 401 }),
      createWebSocket: () => {
        sockets++
        return new FakeWebSocket() as unknown as WebSocket
      },
      createReconciler: async () => ({} as ClientReconciler),
      onAuthenticated: () => { authenticated++ },
      backoffMs: [1],
    })

    await expect(agent.run()).rejects.toThrow('Client Token was rejected')
    expect(authenticated).toBe(0)
    expect(sockets).toBe(0)
  })

  test('reports the previous child state when a Desired Revision fails', async () => {
    const socket = new FakeWebSocket()
    let failApply = false
    const reconciler = {
      apply: async () => {
        if (failApply)
          throw new Error('candidate failed')
      },
      restart: async () => {},
      stop: async () => {},
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: acceptedTokenProbe,
      createWebSocket: () => socket as unknown as WebSocket,
      createReconciler: async () => reconciler,
      backoffMs: [1],
    })
    const running = agent.run()
    await Bun.sleep(0)
    socket.open()
    socket.receive(welcome())
    await Bun.sleep(1)
    agent.reportProcessState('running')
    failApply = true
    socket.receive({
      type: 'desired_state',
      tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
      snapshot: { clientKey: 'client', revision: 1, tunnels: [] },
    })
    for (let attempt = 0; attempt < 20 && !socket.sent.some(value => JSON.parse(value).type === 'apply_result' && !JSON.parse(value).success); attempt++)
      await Bun.sleep(1)
    const messages = socket.sent.map(value => JSON.parse(value))
    expect(messages).toContainEqual(expect.objectContaining({ type: 'apply_result', success: false, revision: 1 }))
    expect(messages.at(-1)).toMatchObject({ type: 'process_state', state: 'running' })
    socket.receive({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'deleted' })
    await running
  })

  test('keeps the applied child running across an ordinary control-link reconnect', async () => {
    const sockets: FakeWebSocket[] = []
    let stops = 0
    let authenticated = 0
    const reconciler = {
      apply: async () => {},
      restart: async () => {},
      stop: async () => { stops++ },
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: acceptedTokenProbe,
      createWebSocket: () => {
        const socket = new FakeWebSocket()
        sockets.push(socket)
        return socket as unknown as WebSocket
      },
      createReconciler: async () => reconciler,
      onAuthenticated: () => { authenticated++ },
      backoffMs: [1],
    })
    const running = agent.run()
    for (let attempt = 0; attempt < 20 && sockets.length < 1; attempt++)
      await Bun.sleep(1)
    sockets[0]!.open()
    sockets[0]!.receive(welcome())
    await Bun.sleep(1)
    agent.reportProcessState('running')
    sockets[0]!.close(1006, 'network outage')
    for (let attempt = 0; attempt < 20 && sockets.length < 2; attempt++)
      await Bun.sleep(1)
    expect(sockets).toHaveLength(2)
    expect(stops).toBe(0)
    sockets[1]!.open()
    sockets[1]!.receive(welcome())
    await Bun.sleep(1)
    expect(sockets[1]!.sent.map(value => JSON.parse(value))).toContainEqual(expect.objectContaining({ type: 'process_state', state: 'running' }))
    expect(authenticated).toBe(1)
    sockets[1]!.receive({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'deleted' })
    await running
    expect(stops).toBe(1)
  })

  test('stops the applied child when a reconnect probe rejects a rotated token', async () => {
    const sockets: FakeWebSocket[] = []
    let probes = 0
    let stops = 0
    const reconciler = {
      apply: async () => {},
      restart: async () => {},
      stop: async () => { stops++ },
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'rotated-token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: async () => new Response(null, { status: probes++ === 0 ? 426 : 401 }),
      createWebSocket: () => {
        const socket = new FakeWebSocket()
        sockets.push(socket)
        return socket as unknown as WebSocket
      },
      createReconciler: async () => reconciler,
      backoffMs: [1],
    })
    const running = agent.run()
    for (let attempt = 0; attempt < 20 && sockets.length < 1; attempt++)
      await Bun.sleep(1)
    sockets[0]!.open()
    sockets[0]!.receive(welcome())
    await Bun.sleep(1)
    sockets[0]!.close(1006, 'network outage')
    await expect(running).rejects.toThrow('Client Token was rejected')
    expect(sockets).toHaveLength(1)
    expect(stops).toBe(1)
  })

  test('processes a Desired Revision received while the welcome is still initializing', async () => {
    const socket = new FakeWebSocket()
    const applied: number[] = []
    let release!: () => void
    const initialized = new Promise<void>(resolve => release = resolve)
    const reconciler = {
      apply: async (input: ReconcileInput) => { applied.push(input.snapshot.revision) },
      restart: async () => {},
      stop: async () => {},
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: acceptedTokenProbe,
      createWebSocket: () => socket as unknown as WebSocket,
      createReconciler: async () => {
        await initialized
        return reconciler
      },
      backoffMs: [1],
    })
    const running = agent.run()
    await Bun.sleep(0)
    socket.open()
    socket.receive(welcome())
    socket.receive({
      type: 'desired_state',
      tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
      snapshot: { clientKey: 'client', revision: 1, tunnels: [] },
    })
    release()
    for (let attempt = 0; attempt < 20 && applied.length < 2; attempt++)
      await Bun.sleep(1)
    expect(applied).toEqual([0, 1])
    socket.receive({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'deleted' })
    await running
  })

  test('does not apply a welcome after revocation arrives during initialization', async () => {
    const socket = new FakeWebSocket()
    let applies = 0
    let stops = 0
    let release!: () => void
    const initialized = new Promise<void>(resolve => release = resolve)
    const reconciler = {
      apply: async () => { applies++ },
      restart: async () => {},
      stop: async () => { stops++ },
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: acceptedTokenProbe,
      createWebSocket: () => socket as unknown as WebSocket,
      createReconciler: async () => {
        await initialized
        return reconciler
      },
      backoffMs: [1],
    })
    const running = agent.run()
    await Bun.sleep(0)
    socket.open()
    socket.receive(welcome())
    socket.receive({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'rotated' })
    release()
    await running
    expect(applies).toBe(0)
    expect(stops).toBe(1)
  })
})
