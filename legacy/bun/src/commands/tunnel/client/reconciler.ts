import type { FrpcDesiredConfiguration } from '../types'
import type { AppliedClientState } from './state'
import { readFile, rm } from 'node:fs/promises'
import path from 'node:path'
import { getLogger } from '../../../shared/log'
import { atomicWrite } from '../atomic-file'
import { renderFrpcConfig, verifyFrpConfiguration } from '../frp/config'
import { TunnelError } from '../types'
import { activeFrpcConfigPath, readAppliedClientState, writeAppliedClientState } from './state'

export interface ClientFrpRuntime {
  verify: (configurationPath: string) => Promise<void>
  start: (configurationPath: string) => Promise<void>
  stop: () => Promise<void>
  restart: () => Promise<void>
}

export type ReconcileInput = FrpcDesiredConfiguration

async function optionalBytes(filePath: string): Promise<Uint8Array | undefined> {
  try {
    return await readFile(filePath)
  }
  catch (cause) {
    if ((cause as NodeJS.ErrnoException).code === 'ENOENT')
      return undefined
    throw cause
  }
}

export class ClientReconciler {
  private queue = Promise.resolve()
  private activated = false
  private stopped = false
  private stopPromise?: Promise<void>
  private readonly logger = getLogger('tunnel.client.reconciler')

  constructor(
    private readonly stateDirectory: string,
    private readonly runtime: ClientFrpRuntime,
  ) {}

  lastApplied(): Promise<AppliedClientState | undefined> {
    return readAppliedClientState(this.stateDirectory)
  }

  apply(input: ReconcileInput): Promise<void> {
    return this.enqueue(() => this.applyNow(input))
  }

  private enqueue(task: () => Promise<void>): Promise<void> {
    const result = this.queue.then(task, task)
    this.queue = result.catch(() => {})
    return result
  }

  private ensureActive(): void {
    if (this.stopped)
      throw new TunnelError('CLIENT_STOPPED', 'Tunnel client is stopping')
  }

  private async applyNow(input: ReconcileInput): Promise<void> {
    this.ensureActive()
    const current = await this.lastApplied()
    if (current && input.snapshot.revision < current.revision)
      return
    if (this.activated && current?.revision === input.snapshot.revision)
      return
    const activePath = activeFrpcConfigPath(this.stateDirectory)
    const candidatePath = path.join(this.stateDirectory, `frpc.revision-${input.snapshot.revision}.candidate.toml`)
    const configuration = renderFrpcConfig(input, this.logger.level)
    const hasEnabledTunnels = input.snapshot.tunnels.some(tunnel => tunnel.enabled)
    this.logger.debug('Applying desired tunnel state', { revision: input.snapshot.revision, enabledTunnels: input.snapshot.tunnels.filter(tunnel => tunnel.enabled).length })
    await atomicWrite(candidatePath, configuration)
    try {
      try {
        if (hasEnabledTunnels)
          await this.runtime.verify(candidatePath)
      }
      catch (cause) {
        this.logger.error('FRP configuration verification failed', cause, { revision: input.snapshot.revision })
        throw new TunnelError('CONFIGURATION_FAILED', cause instanceof Error ? cause.message : String(cause))
      }

      this.ensureActive()
      const previousConfiguration = await optionalBytes(activePath)
      try {
        await this.runtime.stop()
        this.ensureActive()
        await atomicWrite(activePath, configuration)
        this.ensureActive()
        if (hasEnabledTunnels)
          await this.runtime.start(activePath)
        this.ensureActive()
        await writeAppliedClientState(this.stateDirectory, { ...input, revision: input.snapshot.revision })
        this.activated = true
        this.logger.info('Desired tunnel state applied', { revision: input.snapshot.revision, enabledTunnels: input.snapshot.tunnels.filter(tunnel => tunnel.enabled).length })
      }
      catch (cause) {
        this.logger.error('Could not activate desired tunnel state', cause, { revision: input.snapshot.revision })
        await this.runtime.stop().catch(() => {})
        if (previousConfiguration)
          await atomicWrite(activePath, previousConfiguration)
        else
          await rm(activePath, { force: true })
        if (!this.stopped && current?.snapshot.tunnels.some(tunnel => tunnel.enabled) && previousConfiguration)
          await this.runtime.start(activePath).catch(() => {})
        if (cause instanceof TunnelError && cause.code === 'CLIENT_STOPPED')
          throw cause
        throw new TunnelError('ACTIVATION_FAILED', cause instanceof Error ? cause.message : String(cause))
      }
    }
    finally {
      await rm(candidatePath, { force: true })
    }
  }

  restart(): Promise<void> {
    return this.enqueue(async () => {
      if (this.stopped)
        return
      const current = await this.lastApplied()
      if (current?.snapshot.tunnels.some(tunnel => tunnel.enabled))
        await this.runtime.restart()
    })
  }

  stop(): Promise<void> {
    this.stopped = true
    this.activated = false
    this.stopPromise ??= this.enqueue(async () => {
      await this.runtime.stop()
      this.activated = false
    })
    return this.stopPromise
  }
}

export class SupervisorClientRuntime implements ClientFrpRuntime {
  constructor(
    private readonly binaryPath: string,
    private readonly supervisor: { start: (path: string) => Promise<void>, stop: () => Promise<void>, restart: () => Promise<void> },
  ) {}

  async verify(configurationPath: string): Promise<void> {
    await verifyFrpConfiguration(this.binaryPath, configurationPath)
  }

  start(configurationPath: string): Promise<void> {
    return this.supervisor.start(configurationPath)
  }

  stop(): Promise<void> {
    return this.supervisor.stop()
  }

  restart(): Promise<void> {
    return this.supervisor.restart()
  }
}
