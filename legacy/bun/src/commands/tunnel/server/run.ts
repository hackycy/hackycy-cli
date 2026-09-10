import type { FrpSupervisor, FrpSupervisorOptions } from '../frp/supervisor'
import type { ServerTunnelConfig } from '../types'
import process from 'node:process'
import { getLogger } from '../../../shared/log'
import { acquireStateDirectoryLock } from '../lock'
import { AgentGateway } from './agent-gateway'
import { TunnelControlPlane } from './control-plane'
import { openTunnelDatabase } from './database'
import { FrpsConfiguration } from './frps-configuration'
import { startTunnelHttpServer } from './http'
import { ManagedFrpsController } from './managed-frps'
import { TunnelManagement } from './tunnel-management'

export interface TunnelServerRunOptions {
  signal?: AbortSignal
  ensureFrpsBinary?: (signal: AbortSignal) => Promise<string>
  verifyFrpsConfiguration?: (binaryPath: string, configurationPath: string, signal: AbortSignal) => Promise<void>
  createFrpsSupervisor?: (options: FrpSupervisorOptions) => FrpSupervisor
}

export async function runTunnelServer(config: ServerTunnelConfig, options: TunnelServerRunOptions = {}): Promise<void> {
  const logger = getLogger('tunnel.server')
  let lock: Awaited<ReturnType<typeof acquireStateDirectoryLock>>
  try {
    lock = await acquireStateDirectoryLock(config.dataDir)
  }
  catch (cause) {
    logger.error('Could not start tunnel server', cause, { stateDirectory: config.dataDir })
    throw cause
  }
  const shutdown = new AbortController()
  let database: ReturnType<typeof openTunnelDatabase> | undefined
  let gateway: AgentGateway | undefined
  let frps: ManagedFrpsController | undefined
  let frpsConfiguration: FrpsConfiguration | undefined
  let server: ReturnType<typeof startTunnelHttpServer> | undefined
  let management: TunnelManagement | undefined
  const stop = (): void => {
    shutdown.abort()
    gateway?.stop()
    void server?.stop()
  }
  const usesProcessSignals = !options.signal
  if (options.signal?.aborted)
    stop()
  else
    options.signal?.addEventListener('abort', stop, { once: true })
  if (usesProcessSignals) {
    process.once('SIGINT', stop)
    process.once('SIGTERM', stop)
  }

  try {
    if (shutdown.signal.aborted)
      return
    database = openTunnelDatabase(config.dataDir)
    const controlPlane = new TunnelControlPlane(database, config.portRange)
    const frpToken = config.frpToken ?? controlPlane.internalFrpToken()
    frpsConfiguration = new FrpsConfiguration(config)
    frps = new ManagedFrpsController({
      config,
      configuration: frpsConfiguration,
      frpToken,
      signal: shutdown.signal,
      ensureBinary: options.ensureFrpsBinary,
      verifyConfiguration: options.verifyFrpsConfiguration,
      createSupervisor: options.createFrpsSupervisor,
    })
    gateway = new AgentGateway(controlPlane, config.frpPort, frpToken, config.advertiseFrpAddress, frps)
    management = await TunnelManagement.create({ database, controlPlane, gateway, frps, frpsConfiguration, serverConfig: config })
    if (shutdown.signal.aborted)
      return
    server = startTunnelHttpServer({ management, gateway, address: config.address, controlPort: config.controlPort })
    logger.info('Tunnel control plane started', { url: server.url.toString(), address: config.address, controlPort: config.controlPort })
    logger.info('FRP listeners configured', { frpBind: `${config.address}:${config.frpPort}`, httpVhost: `${config.address}:${config.httpPort}`, portPool: `${config.portRange.start}-${config.portRange.end}` })
    logger.info('Tunnel server state directory configured', { stateDirectory: config.dataDir })
    void frps.start().catch(() => {})
    await server.finished
  }
  catch (cause) {
    if (!shutdown.signal.aborted) {
      logger.error('Tunnel server failed', cause)
      throw cause
    }
  }
  finally {
    logger.info('Tunnel server stopping')
    options.signal?.removeEventListener('abort', stop)
    if (usesProcessSignals) {
      process.removeListener('SIGINT', stop)
      process.removeListener('SIGTERM', stop)
    }
    management?.stop()
    gateway?.stop()
    await server?.stop()
    await frps?.stop()
    database?.close()
    await lock.release()
    logger.info('Tunnel server stopped')
  }
}
