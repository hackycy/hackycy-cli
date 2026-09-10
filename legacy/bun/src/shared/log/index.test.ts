import type { LogRecord, LogSink } from './index'
import { describe, expect, test } from 'bun:test'
import { configureLogger, createLogger, formatLogRecord, getLogger, parseLogLevel, resolveLogLevel, stderrLogSink } from './index'

class MemorySink implements LogSink {
  readonly records: LogRecord[] = []

  write(record: LogRecord): void {
    this.records.push(record)
  }
}

describe('shared logger', () => {
  test('filters records below the configured level and preserves child context', () => {
    const sink = new MemorySink()
    const logger = createLogger({ level: 'warn', scope: 'tunnel', context: { role: 'server' }, sink })

    logger.info('hidden')
    logger.child('frps', { pid: 42 }).warn('started')

    expect(sink.records).toHaveLength(1)
    expect(sink.records[0]).toMatchObject({
      level: 'warn',
      scope: 'tunnel.frps',
      message: 'started',
      context: { role: 'server', pid: 42 },
    })
  })

  test('serializes errors and formats structured records as readable text', () => {
    const sink = new MemorySink()
    const logger = createLogger({
      level: 'debug',
      scope: 'tunnel.client',
      sink,
      now: () => new Date('2026-08-18T04:30:00.000Z'),
    })
    logger.error('connection failed\nretrying', new Error('offline'), { attempt: 2 })

    expect(formatLogRecord(sink.records[0]!)).toContain('2026-08-18T04:30:00.000Z ERROR [tunnel.client] connection failed\\nretrying')
    expect(sink.records[0]?.error).toMatchObject({ name: 'Error', message: 'offline' })
    expect(sink.records[0]?.context).toEqual({ attempt: 2 })
  })

  test('colors only the terminal log header when requested', () => {
    const record: LogRecord = {
      timestamp: new Date('2026-08-18T04:30:00.000Z'),
      level: 'error',
      scope: 'tunnel.client',
      message: 'connection failed',
      context: { attempt: 2 },
    }

    const plain = formatLogRecord(record)
    const colored = formatLogRecord(record, true)

    expect(plain).toBe('2026-08-18T04:30:00.000Z ERROR [tunnel.client] connection failed {"attempt":2}')
    expect(plain).not.toContain('\u001B[')
    expect(colored).toContain('\u001B[')
    expect(colored).toContain('connection failed {"attempt":2}')
  })

  test('uses a distinct ANSI color for every log level', () => {
    const record = (level: LogRecord['level']) => formatLogRecord({
      timestamp: new Date('2026-08-18T04:30:00.000Z'),
      level,
      message: 'sample',
    }, true)

    expect(record('debug')).toContain('\u001B[90mDEBUG')
    expect(record('info')).toContain('\u001B[32mINFO ')
    expect(record('warn')).toContain('\u001B[33mWARN ')
    expect(record('error')).toContain('\u001B[1m\u001B[31mERROR')
  })

  test('shares one runtime level and sink across scoped loggers', () => {
    const sink = new MemorySink()
    try {
      const root = configureLogger({
        level: 'warn',
        sink,
        now: () => new Date('2026-08-18T04:30:00.000Z'),
      })
      const server = getLogger('tunnel.server', { role: 'server' })
      const frps = server.child('frps', { pid: 42 })

      expect(root.level).toBe('warn')
      server.info('hidden')
      frps.warn('started')
      expect(sink.records).toHaveLength(1)
      expect(sink.records[0]).toMatchObject({
        level: 'warn',
        scope: 'tunnel.server.frps',
        context: { role: 'server', pid: 42 },
      })

      configureLogger({ level: 'error' })
      expect(root.level).toBe('error')
      frps.warn('hidden after reconfiguration')
      frps.error('failed', new Error('offline'))
      expect(sink.records).toHaveLength(2)
      expect(sink.records[1]).toMatchObject({ level: 'error', message: 'failed' })
    }
    finally {
      configureLogger({ level: 'info', sink: stderrLogSink, now: () => new Date() })
    }
  })

  test('resolves explicit CLI level before environment level', () => {
    expect(resolveLogLevel(undefined, { YCY_LOG_LEVEL: 'debug' })).toBe('debug')
    expect(resolveLogLevel('error', { YCY_LOG_LEVEL: 'debug' })).toBe('error')
    expect(resolveLogLevel(undefined, {})).toBe('info')
    expect(parseLogLevel(' WARN ')).toBe('warn')
    expect(() => parseLogLevel('verbose')).toThrow(/Invalid log level/)
  })

  test('redacts sensitive context keys and message values', () => {
    const sink = new MemorySink()
    const logger = createLogger({ level: 'debug', sink })
    logger.warn('request token=abc password:secret', { clientToken: 'abc', cookie: 'session-value', attempt: 1 })

    expect(sink.records[0]?.message).toBe('request token=[REDACTED] password=[REDACTED]')
    expect(sink.records[0]?.context).toEqual({ clientToken: '[REDACTED]', cookie: '[REDACTED]', attempt: 1 })
    expect(JSON.stringify(sink.records[0])).not.toContain('abc')
    expect(JSON.stringify(sink.records[0])).not.toContain('session-value')
  })
})
