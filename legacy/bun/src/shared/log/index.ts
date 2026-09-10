import process from 'node:process'
import { Ansis } from 'ansis'

export const LOG_LEVELS = ['debug', 'info', 'warn', 'error'] as const
export type LogLevel = typeof LOG_LEVELS[number]

export type LogValue = string | number | boolean | null
export type LogContext = Readonly<Record<string, LogValue | undefined>>

export interface SerializedLogError {
  name: string
  message: string
  stack?: string
}

export interface LogRecord {
  timestamp: Date
  level: LogLevel
  scope?: string
  message: string
  context?: Readonly<Record<string, LogValue>>
  error?: SerializedLogError
}

export interface LogSink {
  write: (record: LogRecord) => void
}

export interface Logger {
  readonly level: LogLevel
  child: (scope: string, context?: LogContext) => Logger
  debug: (message: string, context?: LogContext) => void
  info: (message: string, context?: LogContext) => void
  warn: (message: string, context?: LogContext) => void
  error: (message: string, cause?: unknown, context?: LogContext) => void
}

export interface LoggerOptions {
  level?: LogLevel
  scope?: string
  context?: LogContext
  sink?: LogSink
  now?: () => Date
}

export interface LoggerRuntimeOptions {
  level?: LogLevel
  sink?: LogSink
  now?: () => Date
}

const LOG_LEVEL_RANK: Record<LogLevel, number> = {
  debug: 10,
  info: 20,
  warn: 30,
  error: 40,
}

const SENSITIVE_KEY = /authorization|cookie|password|secret|token/i
const SENSITIVE_ASSIGNMENT = /\b(authorization|cookie|password|secret|token)\s*[:=]\s*([^\s,}"']+)/gi
const BEARER_CREDENTIAL = /(bearer\s+)(\S+)/gi

function redactText(value: string): string {
  return value
    .replace(SENSITIVE_ASSIGNMENT, '$1=[REDACTED]')
    .replace(BEARER_CREDENTIAL, '$1[REDACTED]')
}

function contextValues(context: LogContext | undefined): Record<string, LogValue> | undefined {
  if (!context)
    return undefined
  const values = Object.fromEntries(Object.entries(context)
    .filter(([, value]) => value !== undefined)
    .map(([key, value]) => [key, SENSITIVE_KEY.test(key) ? '[REDACTED]' : value])) as Record<string, LogValue>
  return Object.keys(values).length ? values : undefined
}

function mergeContext(parent: Readonly<Record<string, LogValue>> | undefined, child: LogContext | undefined): Readonly<Record<string, LogValue>> | undefined {
  const values = contextValues(child)
  if (!parent && !values)
    return undefined
  return { ...parent, ...values }
}

function joinScope(parent: string | undefined, child: string): string {
  return parent ? `${parent}.${child}` : child
}

export function parseLogLevel(value: string): LogLevel {
  const normalized = value.trim().toLowerCase()
  if ((LOG_LEVELS as readonly string[]).includes(normalized))
    return normalized as LogLevel
  throw new Error(`Invalid log level '${value}'. Expected one of: ${LOG_LEVELS.join(', ')}`)
}

export function resolveLogLevel(cliValue?: string, env: NodeJS.ProcessEnv = process.env): LogLevel {
  return parseLogLevel(cliValue ?? env.YCY_LOG_LEVEL ?? 'info')
}

export function serializeLogError(cause: unknown): SerializedLogError {
  if (cause instanceof Error) {
    return {
      name: cause.name,
      message: redactText(cause.message),
      ...(cause.stack ? { stack: redactText(cause.stack) } : {}),
    }
  }
  return { name: 'Error', message: redactText(String(cause)) }
}

const COLOR = new Ansis(1)

function formatLogHeader(record: LogRecord, color: boolean): string {
  const timestamp = record.timestamp.toISOString()
  const level = record.level.toUpperCase().padEnd(5)
  const scope = record.scope ? ` [${record.scope}]` : ''

  if (!color)
    return `${timestamp} ${level}${scope}`

  const styledLevel = {
    debug: COLOR.gray(level),
    info: COLOR.green(level),
    warn: COLOR.yellow(level),
    error: COLOR.bold.red(level),
  }[record.level]
  return `${COLOR.dim(timestamp)} ${styledLevel}${scope ? ` ${COLOR.cyan(scope)}` : ''}`
}

export function formatLogRecord(record: LogRecord, color = false): string {
  const prefix = `${formatLogHeader(record, color)} ${record.message.replace(/\r?\n/g, '\\n')}`
  const fields = {
    ...(record.context ?? {}),
    ...(record.error ? { error: record.error } : {}),
  }
  return Object.keys(fields).length ? `${prefix} ${JSON.stringify(fields)}` : prefix
}

export function createStderrLogSink(color = process.stderr.isTTY === true): LogSink {
  return {
    write(record) {
      process.stderr.write(`${formatLogRecord(record, color)}\n`)
    },
  }
}

export const stderrLogSink: LogSink = createStderrLogSink()

interface LoggerRuntime {
  level: LogLevel
  now: () => Date
  sink: LogSink
}

interface LoggerState {
  runtime: LoggerRuntime
  scope?: string
  context?: Readonly<Record<string, LogValue>>
}

class DefaultLogger implements Logger {
  constructor(
    private readonly options: LoggerState,
  ) {
  }

  get level(): LogLevel {
    return this.options.runtime.level
  }

  child(scope: string, context?: LogContext): Logger {
    return new DefaultLogger({
      ...this.options,
      scope: joinScope(this.options.scope, scope),
      context: mergeContext(this.options.context, context),
    })
  }

  debug(message: string, context?: LogContext): void {
    this.write('debug', message, undefined, context)
  }

  info(message: string, context?: LogContext): void {
    this.write('info', message, undefined, context)
  }

  warn(message: string, context?: LogContext): void {
    this.write('warn', message, undefined, context)
  }

  error(message: string, cause?: unknown, context?: LogContext): void {
    this.write('error', message, cause, context)
  }

  private write(level: LogLevel, message: string, cause: unknown, context?: LogContext): void {
    if (LOG_LEVEL_RANK[level] < LOG_LEVEL_RANK[this.level])
      return
    const merged = mergeContext(this.options.context, context)
    this.options.runtime.sink.write({
      timestamp: this.options.runtime.now(),
      level,
      ...(this.options.scope ? { scope: this.options.scope } : {}),
      message: redactText(message),
      ...(merged ? { context: merged } : {}),
      ...(level === 'error' && cause !== undefined ? { error: serializeLogError(cause) } : {}),
    })
  }
}

export function createLogger(options: LoggerOptions = {}): Logger {
  return new DefaultLogger({
    runtime: {
      level: options.level ?? 'info',
      now: options.now ?? (() => new Date()),
      sink: options.sink ?? stderrLogSink,
    },
    ...(options.scope ? { scope: options.scope } : {}),
    ...(options.context ? { context: contextValues(options.context) } : {}),
  })
}

let globalRuntime: LoggerRuntime | undefined
let globalLogger: Logger | undefined

function ensureGlobalLogger(): Logger {
  if (!globalRuntime) {
    globalRuntime = {
      level: resolveLogLevel(),
      now: () => new Date(),
      sink: stderrLogSink,
    }
    globalLogger = new DefaultLogger({ runtime: globalRuntime })
  }
  return globalLogger!
}

export function configureLogger(options: LoggerRuntimeOptions = {}): Logger {
  const logger = ensureGlobalLogger()
  if (options.level !== undefined)
    globalRuntime!.level = options.level
  if (options.sink !== undefined)
    globalRuntime!.sink = options.sink
  if (options.now !== undefined)
    globalRuntime!.now = options.now
  return logger
}

export function getLogger(scope?: string, context?: LogContext): Logger {
  const logger = ensureGlobalLogger()
  if (!scope && !context)
    return logger
  return logger.child(scope ?? '', context)
}
