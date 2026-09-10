import type { FrpSupervisorOptions, FrpSupervisorState } from '../frp/supervisor'
import type { ServerTunnelConfig, StructuredRuntimeError } from '../types'
import type { FrpsConfiguration } from './frps-configuration'
import { getLogger } from '../../../shared/log'
import { atomicWrite } from '../atomic-file'
import { ensureFrpBinary } from '../frp/binary'
import { verifyFrpConfiguration } from '../frp/config'
import { FRPS_ACTIVATION_GRACE_MS, FrpSupervisor } from '../frp/supervisor'
import { TunnelError } from '../types'
import { frpsActivationError } from './frps-activation'

export interface FrpsController {
  state: () => FrpSupervisorState
  observe: (listener: (state: FrpSupervisorState) => void) => () => void
  start: (configPath?: string) => Promise<void>
  stop: () => Promise<void>
  restart: () => Promise<void>
}

export interface ManagedFrpsControllerOptions {
  config: ServerTunnelConfig
  configuration: FrpsConfiguration
  frpToken: string
  signal: AbortSignal
  ensureBinary?: (signal: AbortSignal) => Promise<string>
  verifyConfiguration?: (binaryPath: string, configurationPath: string, signal: AbortSignal) => Promise<void>
  createSupervisor?: (options: FrpSupervisorOptions) => FrpSupervisor
}

function runtimeError(error: TunnelError): StructuredRuntimeError {
  return { code: error.code, message: error.message }
}

function installationError(cause: unknown): TunnelError {
  if (cause instanceof TunnelError)
    return cause
  return new TunnelError('FRP_INSTALL_FAILED', cause instanceof Error ? cause.message : String(cause))
}

function configurationError(cause: unknown): TunnelError {
  if (cause instanceof TunnelError && cause.code === 'CONFIGURATION_FAILED')
    return cause
  return new TunnelError('CONFIGURATION_FAILED', cause instanceof Error ? cause.message : String(cause))
}

export class ManagedFrpsController implements FrpsController {
  private current: FrpSupervisorState = { state: 'stopped' }
  private supervisor: FrpSupervisor | undefined
  private unsubscribeSupervisor: (() => void) | undefined
  private readonly listeners = new Set<(state: FrpSupervisorState) => void>()
  private queue = Promise.resolve()
  private activationPending = false
  private readonly logger = getLogger('tunnel.server.frps')

  constructor(private readonly options: ManagedFrpsControllerOptions) {}

  state(): FrpSupervisorState {
    return { ...this.current, ...(this.current.error ? { error: { ...this.current.error } } : {}) }
  }

  observe(listener: (state: FrpSupervisorState) => void): () => void {
    this.listeners.add(listener)
    listener(this.state())
    return () => this.listeners.delete(listener)
  }

  private publish(state: FrpSupervisorState): void {
    this.current = state
    for (const listener of this.listeners)
      listener(this.state())
  }

  private enqueue(task: () => Promise<void>): Promise<void> {
    const result = this.queue.then(task, task)
    this.queue = result.catch(() => {})
    return result
  }

  private async discardSupervisor(): Promise<void> {
    this.activationPending = false
    const supervisor = this.supervisor
    this.supervisor = undefined
    this.unsubscribeSupervisor?.()
    this.unsubscribeSupervisor = undefined
    await supervisor?.stop()
  }

  private replaceSupervisor(binaryPath: string): FrpSupervisor {
    const supervisor = (this.options.createSupervisor ?? (value => new FrpSupervisor(value)))({
      binaryPath,
      role: 'frps',
      activationGraceMs: FRPS_ACTIVATION_GRACE_MS,
    })
    this.supervisor = supervisor
    this.unsubscribeSupervisor = supervisor.observe((state) => {
      if (!this.activationPending)
        this.publish(state)
    })
    return supervisor
  }

  private async activate(): Promise<void> {
    this.logger.debug('Preparing managed frps')
    await this.discardSupervisor()
    this.publish({ state: 'stopped' })
    let binaryPath: string
    try {
      binaryPath = await (this.options.ensureBinary ?? (signal => ensureFrpBinary('frps', { signal })))(this.options.signal)
      this.logger.debug('Managed frps binary ready', { binaryPath })
    }
    catch (cause) {
      throw installationError(cause)
    }

    const configurationPath = this.options.configuration.tomlPath
    try {
      await atomicWrite(configurationPath, this.options.configuration.render(this.options.frpToken))
      await (this.options.verifyConfiguration ?? ((binary, configuration, signal) => verifyFrpConfiguration(binary, configuration, { signal })))(binaryPath, configurationPath, this.options.signal)
    }
    catch (cause) {
      throw configurationError(cause)
    }

    try {
      this.activationPending = true
      const supervisor = this.replaceSupervisor(binaryPath)
      await supervisor.start(configurationPath)
      this.activationPending = false
      this.publish(supervisor.state())
    }
    catch (cause) {
      this.activationPending = false
      throw cause instanceof TunnelError ? cause : frpsActivationError(this.options.config, cause)
    }
  }

  private async runActivation(): Promise<void> {
    try {
      await this.activate()
    }
    catch (cause) {
      await this.discardSupervisor()
      if (this.options.signal.aborted)
        throw cause
      const error = cause instanceof TunnelError ? cause : frpsActivationError(this.options.config, cause)
      this.logger.error('Managed frps activation failed', error)
      this.publish({ state: 'stopped', error: runtimeError(error) })
      throw error
    }
  }

  start(): Promise<void> {
    return this.enqueue(() => this.runActivation())
  }

  restart(): Promise<void> {
    return this.enqueue(() => this.runActivation())
  }

  stop(): Promise<void> {
    return this.enqueue(async () => {
      await this.discardSupervisor()
      this.publish({ state: 'stopped' })
    })
  }
}
