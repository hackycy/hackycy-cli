import type { AppConfig, TunnelConfig } from './types'
import { randomUUID } from 'node:crypto'
import { mkdir, readFile, rename, rm, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { generateSalt, getConfigDir } from './crypto'

const CONFIG_FILE = 'config.json'
const CONFIG_LOCK = '.config.lock'
const CONFIG_LOCK_TIMEOUT_MS = 10_000
const CONFIG_LOCK_RETRY_MS = 25
const CONFIG_LOCK_PUBLICATION_GRACE_MS = 1000

interface ConfigLockOwner {
  id: string
  pid: number
  startedAt: string
}

interface ConfigLock {
  release: () => Promise<void>
}

export function getConfigPath(env: NodeJS.ProcessEnv = process.env): string {
  return path.join(getConfigDir(env), CONFIG_FILE)
}

function emptyConfig(): AppConfig {
  return {
    salt: generateSalt(),
    fork: {
      instances: {},
    },
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function normalizeConfig(raw: unknown): AppConfig {
  const fallback = emptyConfig()
  if (!isRecord(raw))
    return fallback

  const salt = typeof raw.salt === 'string' && raw.salt ? raw.salt : fallback.salt

  const legacyInstances = isRecord(raw.instances) ? raw.instances : undefined
  const fork = isRecord(raw.fork) ? raw.fork : undefined
  const forkInstances = isRecord(fork?.instances) ? fork.instances : undefined
  const instances = forkInstances ?? legacyInstances ?? {}
  for (const instance of Object.values(instances)) {
    if (isRecord(instance) && typeof instance.host === 'string' && instance.host.includes('://')) {
      const url = new URL(instance.host)
      instance.scheme = url.protocol.slice(0, -1)
      instance.host = url.host
    }
  }

  const cm = isRecord(raw.cm)
    ? raw.cm
    : isRecord(raw.ai)
      ? raw.ai
      : undefined
  const tunnelConfig = isRecord(raw.tunnel) ? raw.tunnel : undefined
  const tunnelConnections = isRecord(tunnelConfig?.connections) ? tunnelConfig.connections : undefined
  const tunnel = tunnelConnections
    ? { connections: tunnelConnections as TunnelConfig['connections'] }
    : undefined
  return {
    salt,
    fork: {
      instances: instances as AppConfig['fork']['instances'],
    },
    cm: cm as AppConfig['cm'],
    tunnel,
  }
}

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

async function readLockOwner(lockDirectory: string): Promise<ConfigLockOwner | undefined> {
  try {
    const owner = JSON.parse(await readFile(path.join(lockDirectory, 'owner.json'), 'utf8')) as ConfigLockOwner
    return typeof owner.id === 'string' && typeof owner.startedAt === 'string' && Number.isSafeInteger(owner.pid)
      ? owner
      : undefined
  }
  catch {
    return undefined
  }
}

async function removeStaleLock(lockDirectory: string): Promise<void> {
  const staleDirectory = `${lockDirectory}.stale-${randomUUID()}`
  try {
    await rename(lockDirectory, staleDirectory)
    await rm(staleDirectory, { recursive: true, force: true })
  }
  catch (cause) {
    if ((cause as NodeJS.ErrnoException).code !== 'ENOENT')
      throw cause
  }
}

async function recentlyCreated(lockDirectory: string): Promise<boolean> {
  try {
    return Date.now() - (await stat(lockDirectory)).mtimeMs < CONFIG_LOCK_PUBLICATION_GRACE_MS
  }
  catch {
    return false
  }
}

async function acquireConfigLock(env: NodeJS.ProcessEnv): Promise<ConfigLock> {
  const configDirectory = getConfigDir(env)
  await mkdir(configDirectory, { recursive: true, mode: 0o700 })
  const lockDirectory = path.join(configDirectory, CONFIG_LOCK)
  const owner: ConfigLockOwner = { id: randomUUID(), pid: process.pid, startedAt: new Date().toISOString() }
  const deadline = Date.now() + CONFIG_LOCK_TIMEOUT_MS

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
        async release() {
          const current = await readLockOwner(lockDirectory)
          if (current?.id === owner.id)
            await rm(lockDirectory, { recursive: true, force: true })
        },
      }
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code !== 'EEXIST')
        throw cause
      const current = await readLockOwner(lockDirectory)
      if (!current || !processExists(current.pid)) {
        if (!current && await recentlyCreated(lockDirectory)) {
          await new Promise(resolve => setTimeout(resolve, CONFIG_LOCK_RETRY_MS))
          continue
        }
        await removeStaleLock(lockDirectory)
        continue
      }
      await new Promise(resolve => setTimeout(resolve, CONFIG_LOCK_RETRY_MS))
    }
  }
  throw new Error(`Could not lock ycy configuration within ${CONFIG_LOCK_TIMEOUT_MS / 1000} seconds`)
}

async function readConfigFile(env: NodeJS.ProcessEnv): Promise<{ config: AppConfig, exists: boolean }> {
  try {
    return { config: normalizeConfig(JSON.parse(await readFile(getConfigPath(env), 'utf8'))), exists: true }
  }
  catch (cause) {
    if ((cause as NodeJS.ErrnoException).code === 'ENOENT')
      return { config: emptyConfig(), exists: false }
    throw cause
  }
}

async function atomicWriteConfig(config: AppConfig, env: NodeJS.ProcessEnv): Promise<void> {
  const target = getConfigPath(env)
  const candidate = `${target}.candidate-${randomUUID()}`
  try {
    await writeFile(candidate, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 })
    await rename(candidate, target)
  }
  finally {
    await rm(candidate, { force: true })
  }
}

export async function readConfig(env: NodeJS.ProcessEnv = process.env): Promise<AppConfig> {
  return (await readConfigFile(env)).config
}

export async function writeConfig(config: AppConfig, env: NodeJS.ProcessEnv = process.env): Promise<void> {
  const lock = await acquireConfigLock(env)
  try {
    await atomicWriteConfig(config, env)
  }
  finally {
    await lock.release()
  }
}

export async function ensureConfig(env: NodeJS.ProcessEnv = process.env): Promise<AppConfig> {
  const lock = await acquireConfigLock(env)
  try {
    const current = await readConfigFile(env)
    if (!current.exists)
      await atomicWriteConfig(current.config, env)
    return current.config
  }
  finally {
    await lock.release()
  }
}

export async function updateConfig<T>(
  mutate: (config: AppConfig) => T | Promise<T>,
  env: NodeJS.ProcessEnv = process.env,
): Promise<T> {
  const lock = await acquireConfigLock(env)
  try {
    const { config } = await readConfigFile(env)
    const result = await mutate(config)
    await atomicWriteConfig(config, env)
    return result
  }
  finally {
    await lock.release()
  }
}
