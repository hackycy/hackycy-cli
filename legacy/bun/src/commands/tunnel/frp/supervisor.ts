import type { FrpProcessState, StructuredRuntimeError } from '../types'
import { getLogger } from '../../../shared/log'
import { backoffDelay, DEFAULT_BACKOFF_MS } from '../backoff'

export interface FrpChild {
  readonly pid: number
  readonly exited: Promise<number>
  readonly stdout?: ReadableStream<Uint8Array>
  readonly stderr?: ReadableStream<Uint8Array>
  kill: (signal?: NodeJS.Signals | number) => void
}

export interface FrpSupervisorState {
  state: FrpProcessState
  pid?: number
  error?: StructuredRuntimeError
}

export interface FrpSupervisorOptions {
  binaryPath: string
  role: 'frpc' | 'frps'
  spawn?: (command: string[]) => FrpChild
  backoffMs?: readonly number[]
  activationGraceMs?: number
  stableAfterMs?: number
  stopTimeoutMs?: number
}

const sleep = (milliseconds: number): Promise<void> => new Promise(resolve => setTimeout(resolve, milliseconds))
export const FRP_ACTIVATION_GRACE_MS = 250
export const FRPS_ACTIVATION_GRACE_MS = 3_000

export class FrpSupervisor {
  private child?: FrpChild
  private configPath?: string
  private desiredRunning = false
  private failureCount = 0
  private retryTimer?: ReturnType<typeof setTimeout>
  private stableTimer?: ReturnType<typeof setTimeout>
  private queue = Promise.resolve()
  private current: FrpSupervisorState = { state: 'stopped' }
  private readonly listeners = new Set<(state: FrpSupervisorState) => void>()
  private readonly spawnChild: (command: string[]) => FrpChild
  private readonly backoffMs: readonly number[]
  private readonly logger: ReturnType<typeof getLogger>

  constructor(private readonly options: FrpSupervisorOptions) {
    this.logger = getLogger(options.role === 'frpc' ? 'tunnel.client.frpc' : 'tunnel.server.frps')
    this.spawnChild = options.spawn ?? (command => Bun.spawn(command, {
      stdin: 'ignore',
      stdout: 'pipe',
      stderr: 'pipe',
    }) as unknown as FrpChild)
    this.backoffMs = options.backoffMs ?? DEFAULT_BACKOFF_MS
  }

  state(): FrpSupervisorState {
    return { ...this.current }
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

  private clearTimers(): void {
    if (this.retryTimer)
      clearTimeout(this.retryTimer)
    if (this.stableTimer)
      clearTimeout(this.stableTimer)
    this.retryTimer = undefined
    this.stableTimer = undefined
  }

  private spawn(): FrpChild {
    if (!this.configPath)
      throw new Error(`Cannot start ${this.options.role} without a configuration path`)
    this.clearTimers()
    this.logger.debug('Starting FRP child', { role: this.options.role, configPath: this.configPath })
    const child = this.spawnChild([this.options.binaryPath, '-c', this.configPath])
    this.child = child
    this.consumeOutput(child.stdout, 'info', child.pid)
    this.consumeOutput(child.stderr, 'warn', child.pid)
    this.logger.info('FRP child started', { role: this.options.role, pid: child.pid })
    this.publish({ state: 'running', pid: child.pid })
    const stableAfterMs = this.options.stableAfterMs ?? 60_000
    this.stableTimer = setTimeout(() => {
      if (this.child === child)
        this.failureCount = 0
    }, stableAfterMs)
    void child.exited.then(
      exitCode => this.enqueue(() => this.handleExit(child, exitCode)),
      cause => this.enqueue(() => this.handleExit(child, -1, cause)),
    )
    return child
  }

  private async confirmActivation(child: FrpChild): Promise<void> {
    const grace = this.options.activationGraceMs ?? 0
    if (grace <= 0)
      return
    let timeout: number | undefined
    const exited: Promise<{ exitCode: number, cause?: unknown }> = child.exited.then(
      exitCode => ({ exitCode }),
      cause => ({ exitCode: -1, cause }),
    )
    const outcome = await Promise.race([
      exited,
      new Promise<undefined>(resolve => timeout = setTimeout(resolve, grace)),
    ])
    if (timeout)
      clearTimeout(timeout)
    if (!outcome)
      return
    if (this.child === child) {
      this.child = undefined
      if (this.stableTimer)
        clearTimeout(this.stableTimer)
      this.stableTimer = undefined
      this.desiredRunning = false
      this.failureCount = 0
      this.publish({ state: 'stopped' })
    }
    this.logger.error(`${this.options.role} exited during startup`, outcome.cause ?? new Error(`exit code ${outcome.exitCode}`), { role: this.options.role, exitCode: outcome.exitCode })
    throw new Error(`${this.options.role} exited with code ${outcome.exitCode} during startup${outcome.cause ? `: ${String(outcome.cause)}` : ''}`)
  }

  private async handleExit(child: FrpChild, exitCode: number, cause?: unknown): Promise<void> {
    if (this.child !== child)
      return
    this.child = undefined
    if (this.stableTimer)
      clearTimeout(this.stableTimer)
    this.stableTimer = undefined
    if (!this.desiredRunning) {
      this.logger.info('FRP child stopped', { role: this.options.role, pid: child.pid, exitCode })
      this.publish({ state: 'stopped' })
      return
    }
    const delay = backoffDelay(this.failureCount, this.backoffMs)
    this.failureCount++
    this.logger.warn('FRP child exited; scheduling recovery', { role: this.options.role, pid: child.pid, exitCode, delayMs: delay })
    this.publish({
      state: 'recovering',
      error: { code: 'FRP_EXITED', message: `${this.options.role} exited with code ${exitCode}${cause ? `: ${String(cause)}` : ''}` },
    })
    this.retryTimer = setTimeout(() => {
      void this.enqueue(async () => {
        if (this.desiredRunning && !this.child)
          this.spawn()
      })
    }, delay)
  }

  private async stopChild(): Promise<void> {
    this.clearTimers()
    const child = this.child
    this.child = undefined
    if (!child)
      return
    this.logger.debug('Stopping FRP child', { role: this.options.role, pid: child.pid })
    child.kill('SIGTERM')
    const timedOut = await Promise.race([
      child.exited.then(() => false, () => false),
      sleep(this.options.stopTimeoutMs ?? 5000).then(() => true),
    ])
    if (timedOut) {
      this.logger.warn('FRP child did not stop before timeout', { role: this.options.role, pid: child.pid })
      child.kill('SIGKILL')
      await child.exited.catch(() => {})
    }
  }

  start(configPath?: string): Promise<void> {
    return this.enqueue(async () => {
      if (!configPath && !this.configPath)
        throw new Error(`${this.options.role} has no applied configuration`)
      const changed = configPath !== undefined && this.configPath !== configPath
      if (configPath)
        this.configPath = configPath
      this.desiredRunning = true
      if (this.child && !changed)
        return
      if (this.child)
        await this.stopChild()
      const child = this.spawn()
      await this.confirmActivation(child)
    })
  }

  stop(): Promise<void> {
    return this.enqueue(async () => {
      this.desiredRunning = false
      this.failureCount = 0
      await this.stopChild()
      this.logger.debug('FRP supervisor stopped', { role: this.options.role })
      this.publish({ state: 'stopped' })
    })
  }

  restart(): Promise<void> {
    return this.enqueue(async () => {
      if (!this.configPath)
        throw new Error(`${this.options.role} has no applied configuration`)
      this.desiredRunning = true
      await this.stopChild()
      const child = this.spawn()
      await this.confirmActivation(child)
    })
  }

  configurationFailed(error: StructuredRuntimeError): Promise<void> {
    return this.enqueue(async () => {
      this.desiredRunning = false
      await this.stopChild()
      this.logger.error('FRP configuration failed', new Error(error.message), { role: this.options.role, code: error.code })
      this.publish({ state: 'configuration_failed', error })
    })
  }

  private consumeOutput(stream: ReadableStream<Uint8Array> | undefined, level: 'info' | 'warn', pid: number): void {
    if (!stream)
      return
    void (async () => {
      const reader = stream.getReader()
      const decoder = new TextDecoder()
      let pending = ''
      try {
        while (true) {
          const next = await reader.read()
          if (next.done)
            break
          pending += decoder.decode(next.value, { stream: true })
          const lines = pending.split(/\r?\n/)
          pending = lines.pop() ?? ''
          for (const line of lines) {
            if (line)
              this.writeChildLine(level, line, pid)
          }
        }
        pending += decoder.decode()
        if (pending)
          this.writeChildLine(level, pending, pid)
      }
      catch (cause) {
        this.logger.warn('Could not read FRP child output', { role: this.options.role, pid, reason: cause instanceof Error ? cause.message : String(cause) })
      }
      finally {
        reader.releaseLock()
      }
    })()
  }

  private writeChildLine(level: 'info' | 'warn', message: string, pid: number): void {
    const context = { role: this.options.role, pid }
    if (level === 'info')
      this.logger.info(message, context)
    else
      this.logger.warn(message, context)
  }
}
