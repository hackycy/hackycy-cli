import type { StateDirectoryLock } from '../lock'
import { readdir, rm, stat } from 'node:fs/promises'
import path from 'node:path'
import { getLogger } from '../../../shared/log'
import { acquireStateDirectoryLock, acquireStateRegistryLock, stateDirectoryIsActive } from '../lock'

const INSTANCE_DIRECTORY_PATTERN = /^v1_[\w-]{43}$/
const INSTANCE_EXPIRY_MS = 90 * 24 * 60 * 60 * 1000

async function cleanupExpiredInstances(stateRoot: string, currentStateDirectory: string, now: number): Promise<void> {
  const logger = getLogger('tunnel.client.state')
  const entries = await readdir(stateRoot, { withFileTypes: true })
  for (const entry of entries) {
    if (!entry.isDirectory() || !INSTANCE_DIRECTORY_PATTERN.test(entry.name))
      continue
    const stateDirectory = path.join(stateRoot, entry.name)
    if (stateDirectory === currentStateDirectory)
      continue
    try {
      const directoryStat = await stat(stateDirectory)
      if (now - directoryStat.mtimeMs < INSTANCE_EXPIRY_MS || await stateDirectoryIsActive(stateDirectory))
        continue
      await rm(stateDirectory, { recursive: true, force: true })
    }
    catch (cause) {
      logger.warn('Could not clean expired tunnel state directory', { stateDirectory, reason: cause instanceof Error ? cause.message : String(cause) })
    }
  }
}

export async function acquireClientInstanceState(stateDirectory: string, now: number = Date.now()): Promise<StateDirectoryLock> {
  if (!INSTANCE_DIRECTORY_PATTERN.test(path.basename(stateDirectory)))
    return acquireStateDirectoryLock(stateDirectory)

  const stateRoot = path.dirname(stateDirectory)
  const registry = await acquireStateRegistryLock(stateRoot)
  let instance: StateDirectoryLock | undefined
  try {
    instance = await acquireStateDirectoryLock(stateDirectory)
    try {
      await cleanupExpiredInstances(stateRoot, stateDirectory, now)
    }
    catch (cause) {
      getLogger('tunnel.client.state').warn('Could not inspect tunnel client state root', { stateRoot, reason: cause instanceof Error ? cause.message : String(cause) })
    }
    return instance
  }
  catch (cause) {
    await instance?.release()
    throw cause
  }
  finally {
    await registry.release()
  }
}
