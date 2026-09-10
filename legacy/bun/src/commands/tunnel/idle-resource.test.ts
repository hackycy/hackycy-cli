import type { ServerWebSocket } from 'bun'
import type { ClientReconciler } from './client/reconciler'
import type { AgentSocketData } from './server/agent-gateway'
import type { AgentWelcomeMessage } from './types'
import { describe, expect, test } from 'bun:test'
import { TunnelClientAgent } from './client/agent'
import { FRP_VERSION, resolveFrpArtifact } from './frp/manifest'
import { AgentGateway } from './server/agent-gateway'
import { TunnelControlPlane } from './server/control-plane'
import { TunnelDatabase } from './server/database'
import { TUNNEL_PROTOCOL_VERSION } from './types'

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

function welcome(): AgentWelcomeMessage {
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
  }
}

describe('Tunnel idle resources', () => {
  test('performs no endpoint probes or FRP operations while an agent connection is idle', async () => {
    const socket = new FakeWebSocket()
    let probes = 0
    let applies = 0
    let restarts = 0
    let applied!: () => void
    const appliedOnce = new Promise<void>(resolve => applied = resolve)
    const reconciler = {
      apply: async () => {
        applies++
        applied()
      },
      restart: async () => { restarts++ },
      stop: async () => {},
    } as unknown as ClientReconciler
    const agent = new TunnelClientAgent({
      server: new URL('http://control.example.com'),
      token: 'token',
      ycyVersion: 'test',
      lastAppliedRevision: 0,
      fetch: async () => {
        probes++
        return new Response(null, { status: 426 })
      },
      createWebSocket: () => socket as unknown as WebSocket,
      createReconciler: async () => reconciler,
    })
    const running = agent.run()
    await Bun.sleep(0)
    socket.open()
    socket.receive(welcome())
    await Promise.race([appliedOnce, Bun.sleep(1000).then(() => {
      throw new Error('Agent did not apply its welcome snapshot')
    })])
    const sentAfterApply = socket.sent.length
    await Bun.sleep(50)

    expect({ probes, applies, restarts, sent: socket.sent.length }).toEqual({ probes: 1, applies: 1, restarts: 0, sent: sentAfterApply })
    socket.receive({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'deleted' })
    await running
  })

  test('retains one latest runtime entry instead of process-state history', () => {
    const database = new TunnelDatabase(':memory:')
    database.sqlite.run(`
      INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
      VALUES('test-owner', 'environment', 'admin', 'admin', 'admin', NULL, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')
    `)
    const controlPlane = new TunnelControlPlane(database, { start: 20000, end: 20001 })
    const client = controlPlane.createClient('test-owner')
    const gateway = new AgentGateway(controlPlane, 7000, 'internal')
    const socket = {
      data: { clientId: client.id, requestHost: 'localhost', phase: 'active', awaitingPong: false },
      send: () => {},
      close: () => {},
      ping: () => {},
    } as unknown as ServerWebSocket<AgentSocketData>
    try {
      gateway.open(socket)
      for (let index = 0; index < 10_000; index++) {
        gateway.message(socket, JSON.stringify({
          type: 'process_state',
          tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
          state: index % 2 ? 'running' : 'recovering',
          ...(index % 2 ? {} : { error: { code: 'FRP_EXITED', message: 'latest only' } }),
        }))
      }

      const runtime = Reflect.get(gateway, 'runtime') as Map<string, Record<string, unknown>>
      expect(runtime.size).toBe(1)
      expect(Object.values(runtime.get(client.id)!).some(Array.isArray)).toBe(false)
      expect(gateway.state(client.id)).toEqual({ connectionState: 'connected', processState: 'running' })
      const tables = database.sqlite.query<{ name: string }, []>('SELECT name FROM sqlite_master WHERE type = \'table\' ORDER BY name').all().map(row => row.name)
      expect(tables).not.toContain('runtime_history')
    }
    finally {
      gateway.stop()
      database.close()
    }
  })
})
