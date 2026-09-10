import { mkdtemp, readdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { FileSessionError, FileSessionManager } from './index'

const directories: string[] = []

async function sessionDirectory(): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), 'ycy-file-session-'))
  directories.push(directory)
  return directory
}

afterEach(async () => {
  await Promise.all(directories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

describe('FileSessionManager', () => {
  test('persists an opaque session without writing its token to disk', async () => {
    const directory = await sessionDirectory()
    const sessions = await FileSessionManager.open({ directory, idleLifetimeMs: 60_000 })
    const revision = sessions.credentialRevision('alice-password')
    const grant = sessions.issue('alice', revision)
    const files = await readdir(directory)
    const source = await Promise.all(files.filter(file => file.endsWith('.json')).map(file => readFile(path.join(directory, file), 'utf8')))

    expect(grant.token).toMatch(/^[\w-]{43}$/)
    expect(files.some(file => file.includes(grant.token))).toBe(false)
    expect(source.join('\n')).not.toContain(grant.token)
    expect(sessions.resume(grant.token, subject => subject === 'alice' ? revision : undefined)?.subject).toBe('alice')
    sessions.close()
  })

  test('restores a session after restart and extends the idle deadline on use', async () => {
    const directory = await sessionDirectory()
    const first = await FileSessionManager.open({ directory, idleLifetimeMs: 80 })
    const revision = first.credentialRevision('alice-password')
    const grant = first.issue('alice', revision)
    first.close()

    await Bun.sleep(30)
    const second = await FileSessionManager.open({ directory, idleLifetimeMs: 80 })
    const resumed = second.resume(grant.token, subject => subject === 'alice' ? second.credentialRevision('alice-password') : undefined)
    expect(resumed?.subject).toBe('alice')

    await Bun.sleep(60)
    expect(second.resume(grant.token, subject => subject === 'alice' ? second.credentialRevision('alice-password') : undefined)).toBeDefined()
    second.close()
  })

  test('expires idle sessions, revokes changed credentials, and notifies observers', async () => {
    const directory = await sessionDirectory()
    const sessions = await FileSessionManager.open({ directory, idleLifetimeMs: 25 })
    const grant = sessions.issue('alice', sessions.credentialRevision('first-password'))
    let revoked = false
    sessions.observe(grant.token, () => revoked = true)

    await Bun.sleep(60)

    expect(sessions.resume(grant.token, () => sessions.credentialRevision('first-password'))).toBeUndefined()
    expect(revoked).toBe(true)

    const changed = sessions.issue('alice', sessions.credentialRevision('first-password'))
    expect(sessions.resume(changed.token, () => sessions.credentialRevision('replacement-password'))).toBeUndefined()
    sessions.close()
  })

  test('evicts least recently used durable sessions at subject and total limits', async () => {
    const directory = await sessionDirectory()
    const sessions = await FileSessionManager.open({ directory, idleLifetimeMs: 60_000, maxSessions: 3, maxSubjectSessions: 2 })
    const revision = sessions.credentialRevision('password')
    const aliceFirst = sessions.issue('alice', revision)
    const aliceSecond = sessions.issue('alice', revision)
    sessions.resume(aliceFirst.token, () => revision)
    const aliceThird = sessions.issue('alice', revision)

    expect(sessions.resume(aliceSecond.token, () => revision)).toBeUndefined()
    expect(sessions.resume(aliceFirst.token, () => revision)).toBeDefined()
    expect(sessions.resume(aliceThird.token, () => revision)).toBeDefined()

    const bobFirst = sessions.issue('bob', revision)
    const bobSecond = sessions.issue('bob', revision)
    expect(sessions.resume(aliceFirst.token, () => revision)).toBeUndefined()
    expect(sessions.resume(aliceThird.token, () => revision)).toBeDefined()
    expect(sessions.resume(bobFirst.token, () => revision)).toBeDefined()
    expect(sessions.resume(bobSecond.token, () => revision)).toBeDefined()
    sessions.close()
  })

  test('prunes corrupt and expired records, applies restrictive permissions, and rejects a second owner', async () => {
    const directory = await sessionDirectory()
    await writeFile(path.join(directory, 'corrupt.json'), '{not-json')
    await writeFile(path.join(directory, 'interrupted.json.tmp-stale'), 'partial')
    const sessions = await FileSessionManager.open({ directory, idleLifetimeMs: 20 })
    const grant = sessions.issue('alice', sessions.credentialRevision('password'))
    const sessionFile = (await readdir(directory)).find(file => file.endsWith('.json'))!

    expect((await stat(directory)).mode & 0o777).toBe(0o700)
    expect((await stat(path.join(directory, '.session-key'))).mode & 0o777).toBe(0o600)
    expect((await stat(path.join(directory, sessionFile))).mode & 0o777).toBe(0o600)
    expect((await readdir(directory)).some(file => file.includes('.tmp-'))).toBe(false)
    await expect(FileSessionManager.open({ directory })).rejects.toThrow('already in use')
    await Bun.sleep(40)
    expect(sessions.resume(grant.token, () => sessions.credentialRevision('password'))).toBeUndefined()
    expect((await readdir(directory)).some(file => file.endsWith('.json'))).toBe(false)
    sessions.close()
  })

  test('rejects issuing and renewing sessions while storage is unavailable', async () => {
    const directory = await sessionDirectory()
    const sessions = await FileSessionManager.open({ directory, idleLifetimeMs: 60_000 })
    const revision = sessions.credentialRevision('password')
    const grant = sessions.issue('alice', revision)
    await rm(directory, { recursive: true, force: true })
    await writeFile(directory, 'unavailable')

    expect(() => sessions.issue('alice', revision)).toThrow(FileSessionError)
    expect(() => sessions.resume(grant.token, () => revision)).toThrow(FileSessionError)
    sessions.close()
  })
})
