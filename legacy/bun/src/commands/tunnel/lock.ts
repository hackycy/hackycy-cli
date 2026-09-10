import { randomUUID } from 'node:crypto'
import { mkdir, readFile, rename, rm, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { TunnelError } from './types'

interface LockOwner {
  id: string
  pid: number
  startedAt: string
  stateDirectory: string
}

export interface StateDirectoryLock {
  owner: LockOwner
  release: () => Promise<void>
}

interface AcquireLockOptions {
  lockName: string
  waitMs: number
  activeError: 'INSTANCE_ACTIVE' | 'LOCK_UNAVAILABLE'
}

const LOCK_PUBLICATION_GRACE_MS = 1000
const LOCK_RETRY_MS = 25

function processExists(pid: number): boolean {
  if (!Number.isSafeInteger(pid) || pid <= 0)
    return false
  try {
    process.kill(pid, 0)
    return true
  }
  catch (cause) {
    return (cause as NodeJS.ErrnoException).code === 'EPERM'
  }
}

async function readOwner(lockDirectory: string): Promise<LockOwner | undefined> {
  try {
    return JSON.parse(await readFile(path.join(lockDirectory, 'owner.json'), 'utf8')) as LockOwner
  }
  catch {
    return undefined
  }
}

async function recentlyCreated(lockDirectory: string): Promise<boolean> {
  try {
    return Date.now() - (await stat(lockDirectory)).mtimeMs < LOCK_PUBLICATION_GRACE_MS
  }
  catch {
    return false
  }
}

async function removeStaleLock(lockDirectory: string): Promise<void> {
  const staleDirectory = `${lockDirectory}.stale-${randomUUID()}`
  try {
    await rename(lockDirectory, staleDirectory)
    await rm(staleDirectory, { recursive: true, force: true })
  }
  catch (cause) {
    const code = (cause as NodeJS.ErrnoException).code
    if (code !== 'ENOENT')
      throw cause
  }
}

async function acquireLock(stateDirectory: string, options: AcquireLockOptions): Promise<StateDirectoryLock> {
  await mkdir(stateDirectory, { recursive: true })
  const lockDirectory = path.join(stateDirectory, options.lockName)
  const owner: LockOwner = {
    id: randomUUID(),
    pid: process.pid,
    startedAt: new Date().toISOString(),
    stateDirectory,
  }
  const deadline = Date.now() + Math.max(options.waitMs, LOCK_PUBLICATION_GRACE_MS)

  while (Date.now() <= deadline) {
    try {
      await mkdir(lockDirectory)
      try {
        await writeFile(path.join(lockDirectory, 'owner.json'), `${JSON.stringify(owner, null, 2)}\n`, { flag: 'wx', mode: 0o600 })
      }
      catch (cause) {
        await rm(lockDirectory, { recursive: true, force: true })
        throw cause
      }
      return {
        owner,
        async release() {
          const current = await readOwner(lockDirectory)
          if (current?.id === owner.id)
            await rm(lockDirectory, { recursive: true, force: true })
        },
      }
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code !== 'EEXIST')
        throw cause
      const active = await readOwner(lockDirectory)
      if (active && processExists(active.pid)) {
        if (options.waitMs > 0 && Date.now() < deadline) {
          await new Promise(resolve => setTimeout(resolve, LOCK_RETRY_MS))
          continue
        }
        throw new TunnelError(
          options.activeError,
          options.activeError === 'INSTANCE_ACTIVE'
            ? `Tunnel supervisor process ${active.pid} already owns state directory ${stateDirectory}`
            : `Could not acquire tunnel state registry ${stateDirectory} within ${options.waitMs / 1000} seconds`,
        )
      }
      if (!active && await recentlyCreated(lockDirectory)) {
        await new Promise(resolve => setTimeout(resolve, LOCK_RETRY_MS))
        continue
      }
      await removeStaleLock(lockDirectory)
    }
  }
  throw new TunnelError('LOCK_UNAVAILABLE', `Could not acquire tunnel state lock ${lockDirectory}`)
}

export async function acquireStateDirectoryLock(stateDirectory: string): Promise<StateDirectoryLock> {
  return acquireLock(stateDirectory, { lockName: '.lock', waitMs: 0, activeError: 'INSTANCE_ACTIVE' })
}

export async function acquireStateRegistryLock(stateRoot: string): Promise<StateDirectoryLock> {
  return acquireLock(stateRoot, { lockName: '.instances.lock', waitMs: 10_000, activeError: 'LOCK_UNAVAILABLE' })
}

export async function stateDirectoryIsActive(stateDirectory: string): Promise<boolean> {
  const lockDirectory = path.join(stateDirectory, '.lock')
  const owner = await readOwner(lockDirectory)
  if (owner && processExists(owner.pid))
    return true
  if (!owner && await recentlyCreated(lockDirectory))
    return true
  await removeStaleLock(lockDirectory)
  return false
}
