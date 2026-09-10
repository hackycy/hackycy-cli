import type { LogRecord, LogSink } from '../../../shared/log'
import type { FrpChild, FrpSupervisorOptions } from '../frp/supervisor'
import type { ServerTunnelConfig } from '../types'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { describe, expect, test } from 'bun:test'
import { configureLogger, stderrLogSink } from '../../../shared/log'
import { FRPS_ACTIVATION_GRACE_MS, FrpSupervisor } from '../frp/supervisor'
import { acquireStateDirectoryLock } from '../lock'
import { TUNNEL_PROTOCOL_VERSION } from '../types'
import { runTunnelServer } from './run'

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

class FakeChild implements FrpChild {
  readonly pid = 42
  readonly exited: Promise<number>
  private exit!: (code: number) => void

  constructor(exitImmediately = false) {
    this.exited = new Promise(resolve => this.exit = resolve)
    if (exitImmediately)
      queueMicrotask(() => this.exit(1))
  }

  kill(): void {
    this.exit(0)
  }
}

function config(dataDir: string, overrides: Partial<ServerTunnelConfig> = {}): ServerTunnelConfig {
  return {
    address: '127.0.0.1',
    controlPort: 17710,
    frpPort: 17701,
    httpPort: 17702,
    portRange: { start: 20200, end: 20202 },
    dataDir,
    adminUser: 'admin',
    adminPassword: 'admin-secret',
    ...overrides,
  }
}

async function waitForHealth(url: string): Promise<void> {
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      if ((await fetch(url)).ok)
        return
    }
    catch {}
    await Bun.sleep(10)
  }
  throw new Error(`Control plane did not start at ${url}`)
}

async function waitFor<T>(attempt: () => Promise<T | undefined>): Promise<T> {
  for (let index = 0; index < 100; index++) {
    const value = await attempt()
    if (value !== undefined)
      return value
    await Bun.sleep(10)
  }
  throw new Error('Condition did not become true')
}

async function login(origin: string): Promise<string> {
  const response = await fetch(new URL('/api/session', origin), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: 'admin-secret' }),
  })
  expect(response.status).toBe(200)
  return response.headers.get('set-cookie')!.split(';')[0]!
}

async function serverState(origin: string, cookie: string): Promise<any> {
  const response = await fetch(new URL('/api/state', origin), { headers: { Cookie: cookie } })
  expect(response.status).toBe(200)
  return response.json()
}

async function agentWelcome(origin: string): Promise<{ socket: WebSocket, message: any }> {
  const cookie = await login(origin)
  const created = await fetch(new URL('/api/clients', origin), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Cookie': cookie, 'Origin': origin },
    body: JSON.stringify({}),
  })
  expect(created.status).toBe(201)
  const client = (await created.json()).client
  const url = new URL('/api/agent', origin)
  url.protocol = 'ws:'
  const BunWebSocket = WebSocket as unknown as { new (url: string | URL, options: Bun.WebSocketOptions): WebSocket }
  const socket = new BunWebSocket(url, { headers: { Authorization: `Bearer ${client.token}` } })
  const message = await new Promise<any>((resolve, reject) => {
    socket.addEventListener('error', reject, { once: true })
    socket.addEventListener('open', () => socket.send(JSON.stringify({
      type: 'hello',
      tunnelProtocolVersion: TUNNEL_PROTOCOL_VERSION,
      ycyVersion: 'test',
      platform: process.platform,
      architecture: process.arch,
      lastAppliedRevision: 0,
    })), { once: true })
    socket.addEventListener('message', event => resolve(JSON.parse(String(event.data))), { once: true })
  })
  return { socket, message }
}

describe('Tunnel server lifecycle', () => {
  test('records a state-lock conflict through the global logger', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-lock-'))
    const owner = await acquireStateDirectoryLock(dataDir)
    const sink = new MemorySink()
    const restoreLogs = captureLogs(sink)
    try {
      let failure: unknown
      try {
        await runTunnelServer(config(dataDir))
      }
      catch (cause) {
        failure = cause
      }
      expect(failure).toBeInstanceOf(Error)
      expect((failure as Error).message).toMatch(/already owns state directory/)
      expect(sink.records).toContainEqual(expect.objectContaining({ level: 'error', message: 'Could not start tunnel server' }))
    }
    finally {
      restoreLogs()
      await owner.release()
      await rm(dataDir, { recursive: true, force: true })
    }
  })

  test('cancels bootstrap and releases its state directory when shutdown is requested', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-run-'))
    const shutdown = new AbortController()
    let bootstrapStarted!: () => void
    const started = new Promise<void>(resolve => bootstrapStarted = resolve)
    let receivedSignal: AbortSignal | undefined
    const config: ServerTunnelConfig = {
      address: '127.0.0.1',
      controlPort: 0,
      frpPort: 17501,
      httpPort: 17502,
      portRange: { start: 20000, end: 20002 },
      dataDir,
      adminUser: 'admin',
      adminPassword: 'admin-secret',
    }
    try {
      const running = runTunnelServer(config, {
        signal: shutdown.signal,
        ensureFrpsBinary: async (signal) => {
          receivedSignal = signal
          bootstrapStarted()
          await new Promise<void>((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true }))
          return '/frps'
        },
      })
      await started
      shutdown.abort()
      await running
      expect(receivedSignal?.aborted).toBe(true)
      const reacquired = await Bun.file(path.join(dataDir, '.lock', 'owner.json')).exists()
      expect(reacquired).toBe(false)
    }
    finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })

  test('keeps the control plane available when initial frps configuration fails and retries it through the API', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-verify-'))
    const shutdown = new AbortController()
    let shouldReject = true
    const config: ServerTunnelConfig = {
      address: '127.0.0.1',
      controlPort: 17600,
      frpPort: 17601,
      httpPort: 17602,
      portRange: { start: 20100, end: 20102 },
      dataDir,
      adminUser: 'admin',
      adminPassword: 'admin-secret',
    }
    try {
      const running = runTunnelServer(config, {
        signal: shutdown.signal,
        ensureFrpsBinary: async () => '/frps',
        verifyFrpsConfiguration: async () => {
          if (shouldReject)
            throw new Error('invalid server configuration')
        },
        createFrpsSupervisor: options => new FrpSupervisor({ ...options, activationGraceMs: 10, spawn: () => new FakeChild() }),
      })
      const origin = `http://${config.address}:${config.controlPort}`
      await waitForHealth(new URL('/healthz', origin).toString())
      const cookie = await login(origin)
      const stopped = await waitFor(async () => {
        const state = await serverState(origin, cookie)
        return state.server.frps.state === 'stopped' && state.server.frps.error ? state : undefined
      })
      expect(stopped.server.frps.error).toMatchObject({ code: 'CONFIGURATION_FAILED', message: 'invalid server configuration' })
      expect(await Bun.file(path.join(dataDir, '.lock', 'owner.json')).exists()).toBe(true)

      const created = await fetch(new URL('/api/clients', origin), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Cookie': cookie, 'Origin': origin },
        body: JSON.stringify({}),
      })
      const client = (await created.json()).client
      const unavailable = await fetch(new URL('/api/agent', origin), { headers: { Authorization: `Bearer ${client.token}` } })
      expect(unavailable.status).toBe(503)
      expect(await unavailable.json()).toMatchObject({ error: { code: 'FRPS_UNAVAILABLE' } })

      shouldReject = false
      const retried = await fetch(new URL('/api/server/frp/start', origin), { method: 'POST', headers: { Cookie: cookie, Origin: origin } })
      expect(retried.status).toBe(200)
      expect((await retried.json()).server.frps).toMatchObject({ state: 'running' })
      const { socket, message } = await agentWelcome(origin)
      expect(message.type).toBe('welcome')
      socket.close()
      shutdown.abort()
      await running
      expect(await Bun.file(path.join(dataDir, '.lock', 'owner.json')).exists()).toBe(false)
    }
    finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })

  test('keeps the control plane available when initial frps binary preparation fails', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-install-'))
    const shutdown = new AbortController()
    let shouldReject = true
    const serverConfig = config(dataDir, { controlPort: 17610, frpPort: 17611, httpPort: 17612 })
    try {
      const running = runTunnelServer(serverConfig, {
        signal: shutdown.signal,
        ensureFrpsBinary: async () => {
          if (shouldReject)
            throw new Error('download unavailable')
          return '/frps'
        },
        verifyFrpsConfiguration: async () => {},
        createFrpsSupervisor: options => new FrpSupervisor({ ...options, activationGraceMs: 10, spawn: () => new FakeChild() }),
      })
      const origin = `http://${serverConfig.address}:${serverConfig.controlPort}`
      await waitForHealth(new URL('/healthz', origin).toString())
      const cookie = await login(origin)
      const stopped = await waitFor(async () => {
        const state = await serverState(origin, cookie)
        return state.server.frps.state === 'stopped' && state.server.frps.error ? state : undefined
      })
      expect(stopped.server.frps.error).toMatchObject({ code: 'FRP_INSTALL_FAILED', message: 'download unavailable' })
      expect(await Bun.file(path.join(dataDir, '.lock', 'owner.json')).exists()).toBe(true)

      shouldReject = false
      const retried = await fetch(new URL('/api/server/frp/restart', origin), { method: 'POST', headers: { Cookie: cookie, Origin: origin } })
      expect(retried.status).toBe(200)
      expect((await retried.json()).server.frps).toMatchObject({ state: 'running' })
      shutdown.abort()
      await running
    }
    finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })

  test('uses a configured FRP token for both managed frps and trusted agents', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-token-'))
    const shutdown = new AbortController()
    const config: ServerTunnelConfig = {
      address: '127.0.0.1',
      controlPort: 17700,
      frpPort: 17701,
      httpPort: 17702,
      portRange: { start: 20200, end: 20202 },
      frpToken: 'external-frp-token',
      dataDir,
      adminUser: 'admin',
      adminPassword: 'admin-secret',
    }
    try {
      const running = runTunnelServer(config, {
        signal: shutdown.signal,
        ensureFrpsBinary: async () => '/frps',
        verifyFrpsConfiguration: async () => {},
        createFrpsSupervisor: (options: FrpSupervisorOptions) => new FrpSupervisor({
          ...options,
          activationGraceMs: 10,
          spawn: () => new FakeChild(),
        }),
      })
      const origin = `http://${config.address}:${config.controlPort}`
      await waitForHealth(new URL('/healthz', origin).toString())
      const cookie = await login(origin)
      await waitFor(async () => (await serverState(origin, cookie)).server.frps.state === 'running' ? true : undefined)
      const source = await readFile(path.join(dataDir, 'frps.toml'), 'utf8')
      expect(source).toContain('token = "external-frp-token"')
      expect(source).toContain(`custom404Page = "${path.join(dataDir, '404.html')}"`)
      const { socket, message } = await agentWelcome(origin)
      expect(message.internalFrpToken).toBe('external-frp-token')
      socket.close()
      shutdown.abort()
      await running
    }
    finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })

  test('uses its persistent FRP token for both managed frps and trusted agents', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-persistent-token-'))
    const shutdown = new AbortController()
    let spawned!: () => void
    const frpsReady = new Promise<void>(resolve => spawned = resolve)
    try {
      const running = runTunnelServer(config(dataDir), {
        signal: shutdown.signal,
        ensureFrpsBinary: async () => '/frps',
        verifyFrpsConfiguration: async () => {},
        createFrpsSupervisor: (options: FrpSupervisorOptions) => {
          expect(options.activationGraceMs).toBe(FRPS_ACTIVATION_GRACE_MS)
          return new FrpSupervisor({
            ...options,
            activationGraceMs: 10,
            spawn: () => {
              spawned()
              return new FakeChild()
            },
          })
        },
      })
      await frpsReady
      const origin = `http://${config(dataDir).address}:${config(dataDir).controlPort}`
      await waitForHealth(new URL('/healthz', origin).toString())
      const cookie = await login(origin)
      await waitFor(async () => (await serverState(origin, cookie)).server.frps.state === 'running' ? true : undefined)
      const source = await readFile(path.join(dataDir, 'frps.toml'), 'utf8')
      const token = /token = "([^"]+)"/.exec(source)?.[1]
      expect(token).toBeTruthy()
      const { socket, message } = await agentWelcome(origin)
      expect(message.internalFrpToken).toBe(token)
      socket.close()
      shutdown.abort()
      await running
    }
    finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })

  test('keeps the control plane and lock when managed frps cannot claim its ports', async () => {
    const dataDir = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-server-frps-conflict-'))
    const shutdown = new AbortController()
    const serverConfig = config(dataDir)
    try {
      const running = runTunnelServer(serverConfig, {
        signal: shutdown.signal,
        ensureFrpsBinary: async () => '/frps',
        verifyFrpsConfiguration: async () => {},
        createFrpsSupervisor: (options: FrpSupervisorOptions) => new FrpSupervisor({
          ...options,
          activationGraceMs: 10,
          backoffMs: [1],
          spawn: () => new FakeChild(true),
        }),
      })
      const origin = `http://${serverConfig.address}:${serverConfig.controlPort}`
      await waitForHealth(new URL('/healthz', origin).toString())
      const cookie = await login(origin)
      const stopped = await waitFor(async () => {
        const state = await serverState(origin, cookie)
        return state.server.frps.state === 'stopped' && state.server.frps.error ? state : undefined
      })
      const message = stopped.server.frps.error.message
      expect(message).toContain(`FRP bind ${serverConfig.address}:${serverConfig.frpPort}`)
      expect(message).toContain(`HTTP vhost ${serverConfig.address}:${serverConfig.httpPort}`)
      expect(message).toContain('Stop any existing frps')
      expect(message).toContain(`lsof -nP -iTCP:${serverConfig.frpPort} -sTCP:LISTEN`)
      expect(message).toContain(`ss -ltnp 'sport = :${serverConfig.httpPort}'`)
      expect(await Bun.file(path.join(dataDir, '.lock', 'owner.json')).exists()).toBe(true)
      shutdown.abort()
      await running
      expect(await Bun.file(path.join(dataDir, '.lock', 'owner.json')).exists()).toBe(false)
    }
    finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })
})
