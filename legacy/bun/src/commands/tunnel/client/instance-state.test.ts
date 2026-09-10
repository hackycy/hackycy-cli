import { access, mkdir, mkdtemp, rm, utimes, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { acquireStateDirectoryLock } from '../lock'
import { TunnelError } from '../types'
import { acquireClientInstanceState } from './instance-state'

const temporaryDirectories: string[] = []

function instanceDirectory(root: string, character: string): string {
  return path.join(root, `v1_${character.repeat(43)}`)
}

async function exists(target: string): Promise<boolean> {
  try {
    await access(target)
    return true
  }
  catch {
    return false
  }
}

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

describe('client instance state', () => {
  test('rejects the same instance while allowing distinct instances in parallel', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-instances-'))
    temporaryDirectories.push(root)
    const firstDirectory = instanceDirectory(root, 'A')
    const secondDirectory = instanceDirectory(root, 'B')
    const thirdDirectory = instanceDirectory(root, 'C')

    const first = await acquireClientInstanceState(firstDirectory)
    try {
      await expect(acquireClientInstanceState(firstDirectory)).rejects.toMatchObject({ code: 'INSTANCE_ACTIVE' })
      const [second, third] = await Promise.all([
        acquireClientInstanceState(secondDirectory),
        acquireClientInstanceState(thirdDirectory),
      ])
      await Promise.all([second.release(), third.release()])
      expect(await Bun.file(path.join(root, '.instances.lock', 'owner.json')).exists()).toBe(false)
    }
    finally {
      await first.release()
    }
  })

  test('removes only expired unlocked versioned instance directories', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-cleanup-'))
    temporaryDirectories.push(root)
    const currentDirectory = instanceDirectory(root, 'A')
    const expiredDirectory = instanceDirectory(root, 'B')
    const recentDirectory = instanceDirectory(root, 'C')
    const activeDirectory = instanceDirectory(root, 'D')
    const staleDirectory = instanceDirectory(root, 'E')
    const unknownDirectory = path.join(root, 'legacy-client-state')
    const legacyFile = path.join(root, 'frpc.toml')
    const now = Date.parse('2026-08-04T00:00:00.000Z')
    const old = new Date(now - 91 * 24 * 60 * 60 * 1000)
    const recent = new Date(now - 89 * 24 * 60 * 60 * 1000)

    await Promise.all([
      mkdir(expiredDirectory),
      mkdir(recentDirectory),
      mkdir(activeDirectory),
      mkdir(path.join(staleDirectory, '.lock'), { recursive: true }),
      mkdir(unknownDirectory),
      writeFile(legacyFile, 'legacy'),
    ])
    await writeFile(path.join(staleDirectory, '.lock', 'owner.json'), JSON.stringify({
      id: 'stale',
      pid: 2147483647,
      startedAt: '2020-01-01T00:00:00.000Z',
      stateDirectory: staleDirectory,
    }))
    const active = await acquireStateDirectoryLock(activeDirectory)
    await Promise.all([
      utimes(expiredDirectory, old, old),
      utimes(recentDirectory, recent, recent),
      utimes(activeDirectory, old, old),
      utimes(staleDirectory, old, old),
      utimes(unknownDirectory, old, old),
    ])

    try {
      const current = await acquireClientInstanceState(currentDirectory, now)
      await current.release()
      expect(await exists(expiredDirectory)).toBe(false)
      expect(await exists(staleDirectory)).toBe(false)
      expect(await exists(recentDirectory)).toBe(true)
      expect(await exists(activeDirectory)).toBe(true)
      expect(await exists(unknownDirectory)).toBe(true)
      expect(await exists(legacyFile)).toBe(true)
    }
    finally {
      await active.release()
    }
  })

  test('recovers a stale state registry lock before acquiring the instance', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-registry-'))
    temporaryDirectories.push(root)
    const stateDirectory = instanceDirectory(root, 'A')
    await mkdir(path.join(root, '.instances.lock'), { recursive: true })
    await writeFile(path.join(root, '.instances.lock', 'owner.json'), JSON.stringify({
      id: 'stale',
      pid: 2147483647,
      startedAt: '2020-01-01T00:00:00.000Z',
      stateDirectory: root,
    }))
    const lock = await acquireClientInstanceState(stateDirectory)
    expect(lock.owner.stateDirectory).toBe(stateDirectory)
    await lock.release()
  })

  test('preserves the structured active-instance error', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-instance-error-'))
    temporaryDirectories.push(root)
    const stateDirectory = instanceDirectory(root, 'A')
    const first = await acquireClientInstanceState(stateDirectory)
    try {
      await acquireClientInstanceState(stateDirectory)
      throw new Error('Expected the duplicate instance to fail')
    }
    catch (cause) {
      expect(cause).toBeInstanceOf(TunnelError)
      expect((cause as TunnelError).code).toBe('INSTANCE_ACTIVE')
    }
    finally {
      await first.release()
    }
  })
})
