import type { AddressInfo } from 'node:net'
import type { ClientReconciler } from './client/reconciler'
import type { AgentWelcomeMessage, ClientRecord, ClientRuntimeState, ServerTunnelConfig, TunnelDefinition, TunnelPresentationState } from './types'
import { Buffer } from 'node:buffer'
import { createSocket } from 'node:dgram'
import { mkdtemp, rm } from 'node:fs/promises'
import { createServer as createHttpServer, request as httpRequest } from 'node:http'
import { connect as connectTcp, createServer as createTcpServer } from 'node:net'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { expect, setDefaultTimeout, test } from 'bun:test'
import { version } from '../../../package.json'
import { TunnelClientAgent } from './client/agent'
import { ClientReconciler as Reconciler, SupervisorClientRuntime } from './client/reconciler'
import { ensureFrpBinary } from './frp/binary'
import { FRP_ACTIVATION_GRACE_MS, FrpSupervisor } from './frp/supervisor'
import { runTunnelServer } from './server/run'

setDefaultTimeout(90_000)

interface ClientView extends ClientRecord {
  runtime: ClientRuntimeState
}

interface ClientDetail {
  client: ClientView
  tunnels: Array<TunnelDefinition & { state: TunnelPresentationState }>
}

interface ManagedAgent {
  agent: TunnelClientAgent
  finished: Promise<{ error?: unknown }>
}

function listeningPort(server: { address: () => string | AddressInfo | null }): number {
  return (server.address() as AddressInfo).port
}

async function listen(server: { listen: (port: number, host: string, callback: () => void) => unknown }): Promise<void> {
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
}

async function close(server: { close: (callback: (error?: Error) => void) => unknown }): Promise<void> {
  await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
}

async function closeUdp(socket: ReturnType<typeof createSocket>): Promise<void> {
  try {
    socket.address()
  }
  catch {
    return
  }
  await new Promise<void>(resolve => socket.close(resolve))
}

async function reserveTcpPorts(count: number): Promise<number[]> {
  const reservations = Array.from({ length: count }, () => createTcpServer())
  try {
    await Promise.all(reservations.map(server => listen(server)))
    return reservations.map(listeningPort)
  }
  finally {
    await Promise.all(reservations.map(server => close(server)))
  }
}

async function reserveTransportPort(): Promise<number> {
  for (let attempt = 0; attempt < 100; attempt++) {
    const candidate = 30_000 + Math.floor(Math.random() * 15_000)
    const tcp = createTcpServer()
    const udp = createSocket('udp4')
    try {
      await Promise.all([
        new Promise<void>((resolve, reject) => tcp.once('error', reject).listen(candidate, '127.0.0.1', resolve)),
        new Promise<void>((resolve, reject) => udp.once('error', reject).bind(candidate, '127.0.0.1', resolve)),
      ])
      await Promise.all([close(tcp), closeUdp(udp)])
      return candidate
    }
    catch {
      if (tcp.listening)
        await close(tcp)
      await closeUdp(udp)
    }
  }
  throw new Error('Could not reserve a TCP/UDP acceptance port')
}

async function waitFor(description: string, condition: () => Promise<boolean>, timeoutMs = 20_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  let lastError: unknown
  while (Date.now() < deadline) {
    try {
      if (await condition())
        return
    }
    catch (cause) {
      lastError = cause
    }
    await Bun.sleep(100)
  }
  throw new Error(`Timed out waiting for ${description}${lastError instanceof Error ? `: ${lastError.message}` : ''}`)
}

async function readHttpResponse(port: number, hostname: string, requestPath = '/'): Promise<{ statusCode: number, body: string }> {
  return await new Promise<{ statusCode: number, body: string }>((resolve, reject) => {
    const request = httpRequest({ hostname: '127.0.0.1', port, path: requestPath, headers: { Host: hostname } }, (response) => {
      const chunks: Uint8Array[] = []
      response.on('data', chunk => chunks.push(chunk))
      response.on('end', () => resolve({ statusCode: response.statusCode ?? 0, body: Buffer.concat(chunks).toString('utf8') }))
    })
    request.once('error', reject)
    request.end()
  })
}

async function readHttpTunnel(port: number, hostname: string, requestPath = '/'): Promise<string> {
  return (await readHttpResponse(port, hostname, requestPath)).body
}

async function readTcpTunnel(port: number, payload: string): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const socket = connectTcp(port, '127.0.0.1')
    const timeout = setTimeout(() => socket.destroy(new Error('TCP tunnel timed out')), 2000)
    socket.once('connect', () => socket.write(payload))
    socket.once('data', (data) => {
      clearTimeout(timeout)
      socket.destroy()
      resolve(data.toString('utf8'))
    })
    socket.once('error', (cause) => {
      clearTimeout(timeout)
      reject(cause)
    })
  })
}

async function readUdpTunnel(port: number, payload: string): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const socket = createSocket('udp4')
    const timeout = setTimeout(() => {
      socket.close()
      reject(new Error('UDP tunnel timed out'))
    }, 2000)
    socket.once('message', (message) => {
      clearTimeout(timeout)
      socket.close()
      resolve(message.toString('utf8'))
    })
    socket.send(payload, port, '127.0.0.1', (cause) => {
      if (cause) {
        clearTimeout(timeout)
        socket.close()
        reject(cause)
      }
    })
  })
}

function startAgent(server: URL, client: ClientRecord, stateDirectory: string, binaryPath: string): ManagedAgent {
  let reconciler: ClientReconciler | undefined
  const agent = new TunnelClientAgent({
    server,
    token: client.token,
    ycyVersion: version,
    lastAppliedRevision: 0,
    async createReconciler(_welcome: AgentWelcomeMessage) {
      if (reconciler)
        return reconciler
      const supervisor = new FrpSupervisor({ binaryPath, role: 'frpc', activationGraceMs: FRP_ACTIVATION_GRACE_MS })
      reconciler = new Reconciler(stateDirectory, new SupervisorClientRuntime(binaryPath, supervisor))
      supervisor.observe(state => agent.reportProcessState(state.state, state.error))
      return reconciler
    },
    backoffMs: [50, 100, 200],
  })
  return {
    agent,
    finished: agent.run().then(() => ({}), error => ({ error })),
  }
}

test('two trusted clients forward HTTP, TCP, and UDP through real pinned FRP processes', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-acceptance-'))
  const reservedPorts = await reserveTcpPorts(3)
  const controlPort = reservedPorts[0]!
  const frpPort = reservedPorts[1]!
  const httpPort = reservedPorts[2]!
  const transportPort = await reserveTransportPort()
  const httpOne = createHttpServer((_request, response) => response.end('client-one'))
  const httpTwo = createHttpServer((_request, response) => response.end('client-two'))
  const httpThree = createHttpServer((_request, response) => response.end('client-three'))
  const httpFour = createHttpServer((_request, response) => response.end('client-four'))
  const tcpEndpoint = createTcpServer(socket => socket.on('data', data => socket.write(`tcp:${data.toString('utf8')}`)))
  const udpEndpoint = createSocket('udp4')
  const serverAbort = new AbortController()
  let serverFinished: Promise<void> | undefined
  const agents: ManagedAgent[] = []
  let agentFailure: unknown

  try {
    await Promise.all([listen(httpOne), listen(httpTwo), listen(httpThree), listen(httpFour), listen(tcpEndpoint)])
    await new Promise<void>((resolve, reject) => udpEndpoint.once('error', reject).bind(0, '127.0.0.1', resolve))
    udpEndpoint.on('message', (message, remote) => udpEndpoint.send(Buffer.from(`udp:${message.toString('utf8')}`), remote.port, remote.address))

    const frpsBinary = await ensureFrpBinary('frps')
    const frpcBinary = await ensureFrpBinary('frpc')
    const config: ServerTunnelConfig = {
      address: '127.0.0.1',
      controlPort,
      frpPort,
      httpPort,
      portRange: { start: transportPort, end: transportPort },
      advertiseFrpAddress: { host: '127.0.0.1', port: frpPort },
      frpToken: 'acceptance-frp-token',
      dataDir: path.join(root, 'server'),
      adminUser: 'admin',
      adminPassword: 'acceptance-secret',
    }
    serverFinished = runTunnelServer(config, { signal: serverAbort.signal, ensureFrpsBinary: async () => frpsBinary })
    const origin = new URL(`http://127.0.0.1:${controlPort}`)
    await waitFor('Tunnel Control Plane', async () => (await fetch(new URL('/healthz', origin))).ok)

    const login = await fetch(new URL('/api/session', origin), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'acceptance-secret' }),
    })
    expect(login.status).toBe(200)
    const cookie = login.headers.get('set-cookie')?.split(';')[0]
    expect(cookie).toBeTruthy()
    const api = async <T>(pathname: string, init: RequestInit = {}): Promise<T> => {
      const response = await fetch(new URL(pathname, origin), {
        ...init,
        headers: {
          Cookie: cookie!,
          Origin: origin.origin,
          ...(init.body ? { 'Content-Type': 'application/json' } : {}),
          ...init.headers,
        },
      })
      if (!response.ok)
        throw new Error(`${init.method ?? 'GET'} ${pathname} returned ${response.status}: ${await response.text()}`)
      return await response.json() as T
    }
    await waitFor('managed frps', async () => (await api<{ server: { frps: { state: string } } }>('/api/state')).server.frps.state === 'running')
    const initialFrpsPid = (await api<{ server: { frps: { pid?: number } } }>('/api/state')).server.frps.pid
    const first404 = '<main>custom-404-first</main>'
    await api('/api/server/frps/config/custom-404-page', { method: 'PUT', body: JSON.stringify({ content: first404 }) })
    await waitFor('the first custom FRP 404 page', async () => {
      const response = await readHttpResponse(httpPort, 'missing.acceptance.test')
      return response.statusCode === 404 && response.body === first404
    })
    const second404 = '<main>custom-404-second</main>'
    await api('/api/server/frps/config/custom-404-page', { method: 'PUT', body: JSON.stringify({ content: second404 }) })
    await waitFor('the updated custom FRP 404 page', async () => (await readHttpResponse(httpPort, 'missing.acceptance.test')).body === second404)
    expect((await api<{ server: { frps: { pid?: number } } }>('/api/state')).server.frps.pid).toBe(initialFrpsPid)
    await api('/api/server/frps/config/custom-404-page', { method: 'PUT', body: JSON.stringify({ content: '' }) })
    await waitFor('the default FRP 404 page', async () => {
      const response = await readHttpResponse(httpPort, 'missing.acceptance.test')
      return response.statusCode === 404 && response.body !== second404
    })

    const clientOne = (await api<{ client: ClientRecord }>('/api/clients', { method: 'POST', body: JSON.stringify({ remark: 'Acceptance client one' }) })).client
    const clientTwo = (await api<{ client: ClientRecord }>('/api/clients', { method: 'POST', body: JSON.stringify({ remark: 'Acceptance client two' }) })).client

    await api(`/api/clients/${clientOne.id}/tunnels`, { method: 'POST', body: JSON.stringify({ label: 'service-a', protocol: 'http', customDomains: ['routes.acceptance.test', 'one.acceptance.test'], location: '/service-a', localPort: listeningPort(httpOne), options: { transport: { useEncryption: true, useCompression: true }, healthCheck: { type: 'http', path: '/health', intervalSeconds: 10, timeoutSeconds: 3, maxFailed: 3 }, http: { hostHeaderRewrite: 'internal.acceptance.test', requestHeaders: [{ name: 'X-Tunnel', value: 'acceptance' }], responseHeaders: [{ name: 'X-Verified', value: 'true' }] } } }) })
    await api(`/api/clients/${clientOne.id}/tunnels`, { method: 'POST', body: JSON.stringify({ protocol: 'tcp', serverPort: transportPort, localPort: listeningPort(tcpEndpoint) }) })
    const serviceB = await api<{ tunnel: TunnelDefinition }>(`/api/clients/${clientTwo.id}/tunnels`, { method: 'POST', body: JSON.stringify({ label: 'service-b', protocol: 'http', customDomains: ['routes.acceptance.test'], location: '/service-b', localPort: listeningPort(httpTwo) }) })
    await api(`/api/clients/${clientTwo.id}/tunnels`, { method: 'POST', body: JSON.stringify({ label: 'service-c', protocol: 'http', customDomains: ['routes.acceptance.test'], location: '/service-c', localPort: listeningPort(httpThree) }) })
    await api(`/api/clients/${clientTwo.id}/tunnels`, { method: 'POST', body: JSON.stringify({ label: 'service-d', protocol: 'http', customDomains: ['routes.acceptance.test'], location: '/service-d', localPort: listeningPort(httpFour) }) })
    await api(`/api/clients/${clientTwo.id}/tunnels`, { method: 'POST', body: JSON.stringify({ protocol: 'udp', serverPort: transportPort, localPort: (udpEndpoint.address() as AddressInfo).port }) })

    agents.push(
      startAgent(origin, clientOne, path.join(root, 'client-one'), frpcBinary),
      startAgent(origin, clientTwo, path.join(root, 'client-two'), frpcBinary),
    )
    await waitFor('both clients to apply their Desired Revisions', async () => {
      const [one, two] = await Promise.all([
        api<ClientDetail>(`/api/clients/${clientOne.id}`),
        api<ClientDetail>(`/api/clients/${clientTwo.id}`),
      ])
      return [one, two].every(detail => detail.client.runtime.processState === 'running' && detail.tunnels.every(tunnel => tunnel.state === 'Applied'))
    })

    await waitFor('the first HTTP Tunnel', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-a/status') === 'client-one')
    await waitFor('the second HTTP Tunnel', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-b/status') === 'client-two')
    await waitFor('the third HTTP Tunnel', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-c/status') === 'client-three')
    await waitFor('the fourth HTTP Tunnel', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-d/status') === 'client-four')
    await waitFor('the HTTP custom-domain alias', async () => await readHttpTunnel(httpPort, 'one.acceptance.test', '/service-a/status') === 'client-one')
    await waitFor('the TCP Tunnel', async () => await readTcpTunnel(transportPort, 'payload') === 'tcp:payload')
    await waitFor('the UDP Tunnel', async () => await readUdpTunnel(transportPort, 'payload') === 'udp:payload')

    await api(`/api/tunnels/${serviceB.tunnel.id}`, { method: 'PATCH', body: JSON.stringify({ enabled: false }) })
    await waitFor('the independently disabled second route', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-b/status') !== 'client-two')
    await waitFor('the third route after the second is disabled', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-c/status') === 'client-three')
    await waitFor('the fourth route after the second is disabled', async () => await readHttpTunnel(httpPort, 'routes.acceptance.test', '/service-d/status') === 'client-four')
  }
  finally {
    await Promise.allSettled(agents.map(({ agent }) => agent.stop()))
    const agentResults = await Promise.all(agents.map(agent => agent.finished))
    serverAbort.abort()
    await serverFinished?.catch(() => {})
    await Promise.allSettled([close(httpOne), close(httpTwo), close(httpThree), close(httpFour), close(tcpEndpoint)])
    await closeUdp(udpEndpoint)
    await rm(root, { recursive: true, force: true })
    agentFailure = agentResults.find(result => result.error)?.error
  }
  if (agentFailure)
    throw agentFailure
})
