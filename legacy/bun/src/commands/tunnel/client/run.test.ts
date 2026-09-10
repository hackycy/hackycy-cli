import type { LogRecord, LogSink } from '../../../shared/log'
import type { ServerToAgentMessage } from '../types'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { describe, expect, test } from 'bun:test'
import { configureLogger, stderrLogSink } from '../../../shared/log'
import { FRP_VERSION, resolveFrpArtifact } from '../frp/manifest'
import { acquireStateDirectoryLock } from '../lock'
import { TUNNEL_PROTOCOL_VERSION } from '../types'
import { runTunnelClient } from './run'

class MemorySink implements LogSink {
  readonly records: LogRecord[] = []

  write(record: LogRecord): void {
    this.records.push(record)
  }
}

function captureLogs(sink: LogSink): () => void {
  configureLogger({ level: 'debug', sink })
  return () => configureLogger({ level: 'info', sink: stderrLogSink })
}

describe('Tunnel client lifecycle', () => {
  test('records a state-lock conflict through the global logger', async () => {
    const stateDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-client-lock-'))
    const owner = await acquireStateDirectoryLock(stateDir)
    const sink = new MemorySink()
    const restoreLogs = captureLogs(sink)
    try {
      let failure: unknown
      try {
        await runTunnelClient({ server: new URL('http://control.example.com'), token: 'secret-token', stateDir })
      }
      catch (cause) {
        failure = cause
      }
      expect(failure).toBeInstanceOf(Error)
      expect((failure as Error).message).toMatch(/already owns state directory/)
      expect(sink.records).toContainEqual(expect.objectContaining({ level: 'error', message: 'Could not start tunnel client' }))
    }
    finally {
      restoreLogs()
      await owner.release()
      await rm(stateDir, { recursive: true, force: true })
    }
  })

  test('cancels FRP bootstrap and releases its state directory when shutdown is requested', async () => {
    const stateDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-client-run-'))
    const shutdown = new AbortController()
    const artifact = resolveFrpArtifact()
    let socket: Bun.ServerWebSocket<undefined> | undefined
    let bootstrapStarted!: () => void
    const started = new Promise<void>(resolve => bootstrapStarted = resolve)
    let receivedSignal: AbortSignal | undefined
    const controlServer = Bun.serve({
      port: 0,
      fetch(request, server) {
        if (request.headers.get('Upgrade')?.toLowerCase() !== 'websocket')
          return new Response(null, { status: 426 })
        if (server.upgrade(request))
          return undefined
        return new Response(null, { status: 500 })
      },
      websocket: {
        open(value) {
          socket = value
        },
        message(value) {
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
            advertisedFrpHost: '127.0.0.1',
            advertisedFrpPort: 7000,
            internalFrpToken: 'internal',
            snapshot: { clientKey: 'client', revision: 0, tunnels: [] },
          }
          value.send(JSON.stringify(welcome))
        },
      },
    })
    let running: Promise<void> | undefined
    const sink = new MemorySink()
    const restoreLogs = captureLogs(sink)
    try {
      running = runTunnelClient({ server: new URL(controlServer.url), token: 'token', stateDir }, {
        signal: shutdown.signal,
        onAuthenticated: async () => { throw new Error('configuration is read only') },
        ensureFrpcBinary: async (signal) => {
          receivedSignal = signal
          bootstrapStarted()
          await new Promise<void>((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true }))
          return '/frpc'
        },
      })
      await Promise.race([
        started,
        Bun.sleep(1000).then(() => { throw new Error('FRP bootstrap did not start') }),
      ])
      shutdown.abort()
      await running
      expect(receivedSignal?.aborted).toBe(true)
      expect(await Bun.file(path.join(stateDir, '.lock', 'owner.json')).exists()).toBe(false)
      expect(sink.records).toContainEqual(expect.objectContaining({
        level: 'warn',
        message: 'Could not remember tunnel connection',
        context: { reason: 'configuration is read only' },
      }))
      expect(JSON.stringify(sink.records)).not.toContain('"token"')
    }
    finally {
      restoreLogs()
      shutdown.abort()
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'revoke', tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION, reason: 'deleted' } satisfies ServerToAgentMessage))
      }
      await Promise.race([running?.catch(() => {}), Bun.sleep(2000)])
      controlServer.stop(true)
      await rm(stateDir, { recursive: true, force: true })
    }
  })
})
