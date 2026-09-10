import type { LogRecord, LogSink } from '../../../shared/log'
import type { FrpChild } from './supervisor'
import { describe, expect, test } from 'bun:test'
import { configureLogger, stderrLogSink } from '../../../shared/log'
import { FrpSupervisor } from './supervisor'

class FakeChild implements FrpChild {
  readonly pid: number
  readonly exited: Promise<number>
  private exit!: (code: number) => void

  constructor(pid: number) {
    this.pid = pid
    this.exited = new Promise(resolve => this.exit = resolve)
  }

  kill(): void {
    this.exit(0)
  }

  crash(code = 1): void {
    this.exit(code)
  }
}

class MemorySink implements LogSink {
  readonly records: LogRecord[] = []

  write(record: LogRecord): void {
    this.records.push(record)
  }
}

function output(text: string): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(text))
      controller.close()
    },
  })
}

describe('FrpSupervisor', () => {
  test('captures child stdout and stderr through the global logger', async () => {
    const sink = new MemorySink()
    configureLogger({ level: 'debug', sink })
    const child = new FakeChild(0) as FrpChild & { stdout?: ReadableStream<Uint8Array>, stderr?: ReadableStream<Uint8Array> }
    Object.assign(child, { stdout: output('frpc ready\n'), stderr: output('frpc warning\n') })
    const supervisor = new FrpSupervisor({
      binaryPath: '/frpc',
      role: 'frpc',
      spawn: () => child,
    })
    try {
      await supervisor.start('/config')
      await Bun.sleep(1)

      expect(sink.records).toContainEqual(expect.objectContaining({ level: 'info', message: 'frpc ready', context: { role: 'frpc', pid: 0 } }))
      expect(sink.records).toContainEqual(expect.objectContaining({ level: 'warn', message: 'frpc warning', context: { role: 'frpc', pid: 0 } }))
      await supervisor.stop()
    }
    finally {
      configureLogger({ level: 'info', sink: stderrLogSink })
    }
  })

  test('owns at most one child and suppresses recovery after manual stop', async () => {
    const children: FakeChild[] = []
    const supervisor = new FrpSupervisor({
      binaryPath: '/frpc',
      role: 'frpc',
      backoffMs: [1],
      stableAfterMs: 1000,
      spawn: () => {
        const child = new FakeChild(children.length + 1)
        children.push(child)
        return child
      },
    })
    await supervisor.start('/config')
    await supervisor.start('/config')
    expect(children).toHaveLength(1)
    children[0]!.crash()
    await Bun.sleep(10)
    expect(children).toHaveLength(2)
    await supervisor.stop()
    await Bun.sleep(10)
    expect(children).toHaveLength(2)
    expect(supervisor.state().state).toBe('stopped')
  })

  test('does not restart deterministic configuration failures', async () => {
    const children: FakeChild[] = []
    const supervisor = new FrpSupervisor({ binaryPath: '/frpc', role: 'frpc', backoffMs: [1], spawn: () => {
      const child = new FakeChild(children.length + 1)
      children.push(child)
      return child
    } })
    await supervisor.start('/config')
    await supervisor.configurationFailed({ code: 'INVALID', message: 'bad config' })
    await Bun.sleep(5)
    expect(children).toHaveLength(1)
    expect(supervisor.state()).toMatchObject({ state: 'configuration_failed', error: { code: 'INVALID' } })
  })

  test('rejects a child that exits during the activation grace period', async () => {
    const children: FakeChild[] = []
    const supervisor = new FrpSupervisor({
      binaryPath: '/frpc',
      role: 'frpc',
      backoffMs: [1],
      activationGraceMs: 10,
      spawn: () => {
        const child = new FakeChild(children.length + 1)
        children.push(child)
        queueMicrotask(() => child.crash(1))
        return child
      },
    })

    await expect(supervisor.start('/candidate')).rejects.toThrow('exited with code 1 during startup')
    await Bun.sleep(15)
    expect(children).toHaveLength(1)
    expect(supervisor.state().state).toBe('stopped')
  })

  test('rejects a restart when its child exits during the activation grace period', async () => {
    const children: FakeChild[] = []
    const supervisor = new FrpSupervisor({
      binaryPath: '/frps',
      role: 'frps',
      backoffMs: [1],
      activationGraceMs: 10,
      spawn: () => {
        const child = new FakeChild(children.length + 1)
        children.push(child)
        if (children.length === 2)
          queueMicrotask(() => child.crash(1))
        return child
      },
    })

    await supervisor.start('/server.toml')
    await expect(supervisor.restart()).rejects.toThrow('exited with code 1 during startup')
    await Bun.sleep(15)
    expect(children).toHaveLength(2)
    expect(supervisor.state().state).toBe('stopped')
  })
})
