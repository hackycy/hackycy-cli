import type { Buffer } from 'node:buffer'
import { createHash, createHmac, randomBytes, randomUUID } from 'node:crypto'
import { chmodSync, closeSync, existsSync, fchmodSync, fsyncSync, mkdirSync, openSync, readdirSync, readFileSync, renameSync, rmSync, unlinkSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import process from 'node:process'

const DEFAULT_IDLE_LIFETIME_MS = 7 * 24 * 60 * 60 * 1000
const DEFAULT_MAX_SUBJECT_SESSIONS = 8
const DEFAULT_MAX_SESSIONS = 128
const SESSION_VERSION = 1
const SESSION_KEY_FILE = '.session-key'
const SESSION_LOCK_FILE = '.session.lock'

interface StoredSession {
  version: number
  tokenHash: string
  subject: string
  revision: string
  createdAt: string
  lastAccessAt: string
  expiresAt: string
}

interface SessionEntry extends StoredSession {
  path: string
}

export interface FileSession {
  token: string
  subject: string
  expiresAt: string
}

export interface FileSessionManagerOptions {
  directory: string
  idleLifetimeMs?: number
  maxSubjectSessions?: number
  maxSessions?: number
  now?: () => number
}

export class FileSessionError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'FileSessionError'
  }
}

function tokenHash(token: string): string {
  return createHash('sha256').update(token).digest('hex')
}

function validTimestamp(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value))
}

function parseStoredSession(value: unknown, filePath: string): SessionEntry | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    return undefined
  const record = value as Partial<StoredSession>
  if (record.version !== SESSION_VERSION
    || typeof record.tokenHash !== 'string' || !/^[a-f\d]{64}$/.test(record.tokenHash)
    || typeof record.subject !== 'string' || !record.subject
    || typeof record.revision !== 'string' || !record.revision
    || !validTimestamp(record.createdAt) || !validTimestamp(record.lastAccessAt) || !validTimestamp(record.expiresAt)) {
    return undefined
  }
  if (path.basename(filePath) !== `${record.tokenHash}.json`)
    return undefined
  return {
    version: record.version,
    tokenHash: record.tokenHash,
    subject: record.subject,
    revision: record.revision,
    createdAt: record.createdAt,
    lastAccessAt: record.lastAccessAt,
    expiresAt: record.expiresAt,
    path: filePath,
  }
}

function processExists(pid: unknown): boolean {
  if (typeof pid !== 'number' || !Number.isSafeInteger(pid) || pid <= 0)
    return false
  try {
    process.kill(pid, 0)
    return true
  }
  catch (cause) {
    return (cause as NodeJS.ErrnoException).code === 'EPERM'
  }
}

function readLockOwner(lockPath: string): { id?: unknown, pid?: unknown } | undefined {
  try {
    const value: unknown = JSON.parse(readFileSync(lockPath, 'utf8'))
    return value && typeof value === 'object' ? value as { id?: unknown, pid?: unknown } : undefined
  }
  catch {
    return undefined
  }
}

export class FileSessionManager {
  private readonly sessions = new Map<string, SessionEntry>()
  private readonly observers = new Map<string, Set<() => void>>()
  private expirationTimer: ReturnType<typeof setTimeout> | undefined
  private closed = false

  private constructor(
    readonly directory: string,
    private readonly key: Buffer,
    private readonly lockPath: string,
    private readonly lockId: string,
    private readonly options: Required<Omit<FileSessionManagerOptions, 'directory'>>,
  ) {}

  static async open(input: FileSessionManagerOptions): Promise<FileSessionManager> {
    const directory = path.resolve(input.directory)
    const options = {
      idleLifetimeMs: Math.max(1, input.idleLifetimeMs ?? DEFAULT_IDLE_LIFETIME_MS),
      maxSubjectSessions: Math.max(1, input.maxSubjectSessions ?? DEFAULT_MAX_SUBJECT_SESSIONS),
      maxSessions: Math.max(1, input.maxSessions ?? DEFAULT_MAX_SESSIONS),
      now: input.now ?? Date.now,
    }
    try {
      mkdirSync(directory, { recursive: true, mode: 0o700 })
      chmodSync(directory, 0o700)
      const lock = this.acquireLock(directory)
      try {
        const key = this.readOrCreateKey(directory)
        const manager = new FileSessionManager(directory, key, lock.path, lock.id, options)
        manager.loadStoredSessions()
        return manager
      }
      catch (cause) {
        this.releaseLock(lock.path, lock.id)
        throw cause
      }
    }
    catch (cause) {
      if (cause instanceof FileSessionError)
        throw cause
      throw new FileSessionError(`File session storage is unavailable at ${directory}`, { cause })
    }
  }

  private static acquireLock(directory: string): { path: string, id: string } {
    const lockPath = path.join(directory, SESSION_LOCK_FILE)
    const owner = { id: randomUUID(), pid: process.pid }
    for (let attempt = 0; attempt < 2; attempt++) {
      try {
        const descriptor = openSync(lockPath, 'wx', 0o600)
        try {
          writeFileSync(descriptor, `${JSON.stringify(owner)}\n`)
          fsyncSync(descriptor)
        }
        finally {
          closeSync(descriptor)
        }
        return { path: lockPath, id: owner.id }
      }
      catch (cause) {
        if ((cause as NodeJS.ErrnoException).code !== 'EEXIST')
          throw cause
        const active = readLockOwner(lockPath)
        if (processExists(active?.pid))
          throw new FileSessionError(`File session directory is already in use: ${directory}`)
        try {
          unlinkSync(lockPath)
        }
        catch (unlinkCause) {
          if ((unlinkCause as NodeJS.ErrnoException).code !== 'ENOENT')
            throw unlinkCause
        }
      }
    }
    throw new FileSessionError(`Could not acquire file session directory: ${directory}`)
  }

  private static releaseLock(lockPath: string, lockId: string): void {
    if (readLockOwner(lockPath)?.id === lockId) {
      try {
        unlinkSync(lockPath)
      }
      catch (cause) {
        if ((cause as NodeJS.ErrnoException).code !== 'ENOENT')
          throw cause
      }
    }
  }

  private static readOrCreateKey(directory: string): Buffer {
    const keyPath = path.join(directory, SESSION_KEY_FILE)
    try {
      const key = readFileSync(keyPath)
      if (key.byteLength !== 32)
        throw new FileSessionError(`File session key is invalid: ${keyPath}`)
      chmodSync(keyPath, 0o600)
      return key
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code !== 'ENOENT')
        throw cause
    }

    const key = randomBytes(32)
    try {
      const descriptor = openSync(keyPath, 'wx', 0o600)
      try {
        writeFileSync(descriptor, key)
        fsyncSync(descriptor)
      }
      finally {
        closeSync(descriptor)
      }
      return key
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code !== 'EEXIST')
        throw cause
      const existing = readFileSync(keyPath)
      if (existing.byteLength !== 32)
        throw new FileSessionError(`File session key is invalid: ${keyPath}`)
      return existing
    }
  }

  credentialRevision(value: string): string {
    return createHmac('sha256', this.key).update(value).digest('base64url')
  }

  private now(): number {
    return this.options.now()
  }

  private sessionPath(hash: string): string {
    return path.join(this.directory, `${hash}.json`)
  }

  private loadStoredSessions(): void {
    try {
      for (const directoryEntry of readdirSync(this.directory, { withFileTypes: true })) {
        const filePath = path.join(this.directory, directoryEntry.name)
        if (directoryEntry.name.includes('.tmp-')) {
          if (directoryEntry.isFile())
            rmSync(filePath, { force: true })
          continue
        }
        if (!directoryEntry.isFile() || !directoryEntry.name.endsWith('.json'))
          continue
        let entry: SessionEntry | undefined
        try {
          entry = parseStoredSession(JSON.parse(readFileSync(filePath, 'utf8')), filePath)
        }
        catch {}
        if (!entry || Date.parse(entry.expiresAt) <= this.now()) {
          rmSync(filePath, { force: true })
          continue
        }
        chmodSync(filePath, 0o600)
        this.sessions.set(entry.tokenHash, entry)
      }
      this.enforceLimits()
      this.scheduleExpiration()
    }
    catch (cause) {
      throw new FileSessionError(`Could not load file sessions from ${this.directory}`, { cause })
    }
  }

  private write(entry: SessionEntry): void {
    const candidate = `${entry.path}.tmp-${randomUUID()}`
    try {
      const descriptor = openSync(candidate, 'wx', 0o600)
      try {
        writeFileSync(descriptor, `${JSON.stringify({
          version: entry.version,
          tokenHash: entry.tokenHash,
          subject: entry.subject,
          revision: entry.revision,
          createdAt: entry.createdAt,
          lastAccessAt: entry.lastAccessAt,
          expiresAt: entry.expiresAt,
        })}\n`)
        fchmodSync(descriptor, 0o600)
        fsyncSync(descriptor)
      }
      finally {
        closeSync(descriptor)
      }
      renameSync(candidate, entry.path)
      chmodSync(entry.path, 0o600)
    }
    catch (cause) {
      throw new FileSessionError(`Could not persist file session at ${entry.path}`, { cause })
    }
    finally {
      if (existsSync(candidate))
        rmSync(candidate, { force: true })
    }
  }

  private delete(entry: SessionEntry): void {
    try {
      rmSync(entry.path, { force: true })
    }
    catch (cause) {
      throw new FileSessionError(`Could not revoke file session at ${entry.path}`, { cause })
    }
  }

  private notify(hash: string): void {
    const listeners = this.observers.get(hash)
    this.observers.delete(hash)
    if (listeners) {
      for (const listener of [...listeners])
        listener()
    }
  }

  private revokeHash(hash: string, schedule = true): void {
    const entry = this.sessions.get(hash)
    if (!entry)
      return
    this.delete(entry)
    this.sessions.delete(hash)
    this.notify(hash)
    if (schedule)
      this.scheduleExpiration()
  }

  private oldest(entries: Iterable<[string, SessionEntry]> = this.sessions): [string, SessionEntry] | undefined {
    return [...entries].sort(([, left], [, right]) => Date.parse(left.lastAccessAt) - Date.parse(right.lastAccessAt))[0]
  }

  private enforceLimits(subject?: string, reserve = false): void {
    const subjects = subject ? [subject] : [...new Set([...this.sessions.values()].map(entry => entry.subject))]
    for (const currentSubject of subjects) {
      const subjectEntries = [...this.sessions].filter(([, entry]) => entry.subject === currentSubject)
      while (subjectEntries.length > this.options.maxSubjectSessions - (reserve ? 1 : 0)) {
        const oldest = this.oldest(subjectEntries)
        if (!oldest)
          break
        this.revokeHash(oldest[0], false)
        subjectEntries.splice(subjectEntries.findIndex(([hash]) => hash === oldest[0]), 1)
      }
    }
    while (this.sessions.size > this.options.maxSessions - (reserve ? 1 : 0)) {
      const oldest = this.oldest()
      if (!oldest)
        break
      this.revokeHash(oldest[0], false)
    }
  }

  private clearExpired(): void {
    const now = this.now()
    for (const [hash, entry] of this.sessions) {
      if (Date.parse(entry.expiresAt) <= now)
        this.revokeHash(hash, false)
    }
    this.scheduleExpiration()
  }

  private scheduleExpiration(): void {
    clearTimeout(this.expirationTimer)
    this.expirationTimer = undefined
    if (this.closed)
      return
    const next = [...this.sessions].sort(([, left], [, right]) => Date.parse(left.expiresAt) - Date.parse(right.expiresAt))[0]
    if (!next)
      return
    const delay = Math.max(0, Date.parse(next[1].expiresAt) - this.now())
    this.expirationTimer = setTimeout(() => this.clearExpired(), delay)
    this.expirationTimer.unref?.()
  }

  issue(subject: string, revision: string): FileSession {
    if (this.closed)
      throw new FileSessionError('File session manager is closed')
    if (!subject || !revision)
      throw new FileSessionError('File session subject and credential revision are required')
    this.clearExpired()
    this.enforceLimits(subject, true)
    let token: string
    let hash: string
    do {
      token = randomBytes(32).toString('base64url')
      hash = tokenHash(token)
    } while (this.sessions.has(hash))
    const now = new Date(this.now()).toISOString()
    const entry: SessionEntry = {
      version: SESSION_VERSION,
      tokenHash: hash,
      subject,
      revision,
      createdAt: now,
      lastAccessAt: now,
      expiresAt: new Date(this.now() + this.options.idleLifetimeMs).toISOString(),
      path: this.sessionPath(hash),
    }
    this.write(entry)
    this.sessions.set(hash, entry)
    this.scheduleExpiration()
    return { token, subject, expiresAt: entry.expiresAt }
  }

  resume(token: string | undefined, currentRevision: (subject: string) => string | undefined): FileSession | undefined {
    if (this.closed || !token)
      return undefined
    this.clearExpired()
    const hash = tokenHash(token)
    const entry = this.sessions.get(hash)
    if (!entry)
      return undefined
    if (currentRevision(entry.subject) !== entry.revision) {
      this.revokeHash(hash)
      return undefined
    }
    const timestamp = this.now()
    entry.lastAccessAt = new Date(timestamp).toISOString()
    entry.expiresAt = new Date(timestamp + this.options.idleLifetimeMs).toISOString()
    this.write(entry)
    this.sessions.delete(hash)
    this.sessions.set(hash, entry)
    this.scheduleExpiration()
    return { token, subject: entry.subject, expiresAt: entry.expiresAt }
  }

  revoke(token: string | undefined): void {
    if (token)
      this.revokeHash(tokenHash(token))
  }

  revokeSubject(subject: string): void {
    for (const [hash, entry] of [...this.sessions]) {
      if (entry.subject === subject)
        this.revokeHash(hash, false)
    }
    this.scheduleExpiration()
  }

  observe(token: string, listener: () => void): () => void {
    const hash = tokenHash(token)
    if (!this.sessions.has(hash))
      return () => {}
    const listeners = this.observers.get(hash) ?? new Set()
    listeners.add(listener)
    this.observers.set(hash, listeners)
    return () => {
      listeners.delete(listener)
      if (!listeners.size)
        this.observers.delete(hash)
    }
  }

  close(): void {
    if (this.closed)
      return
    this.closed = true
    clearTimeout(this.expirationTimer)
    this.expirationTimer = undefined
    this.observers.clear()
    FileSessionManager.releaseLock(this.lockPath, this.lockId)
  }
}
