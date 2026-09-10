import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { createFsAuthentication } from './authentication'

const directories: string[] = []

async function sessionDirectory(): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), 'ycy-fs-auth-'))
  directories.push(directory)
  return directory
}

afterEach(async () => {
  await Promise.all(directories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

describe('FsAuthentication', () => {
  test('parses account specifications and matches usernames without case sensitivity', async () => {
    const authentication = (await createFsAuthentication(['Alice:password:with-colon'], { directory: await sessionDirectory() }))!

    const grant = await authentication.signIn({ username: 'ALICE', password: 'password:with-colon' })

    expect(grant?.account).toEqual({ username: 'Alice' })
    expect(authentication.resume(grant?.token)?.account).toEqual({ username: 'Alice' })
    authentication.close()
  })

  test('accepts a five-character account password', async () => {
    const authentication = (await createFsAuthentication(['alice:12345'], { directory: await sessionDirectory() }))!

    expect((await authentication.signIn({ username: 'alice', password: '12345' }))?.account).toEqual({ username: 'alice' })
    authentication.close()
  })

  test('rejects invalid or duplicate account specifications before startup', async () => {
    const options = { directory: await sessionDirectory() }
    await expect(createFsAuthentication(['alice-password123'], options)).rejects.toThrow('must use')
    await expect(createFsAuthentication(['bad name:password123'], options)).rejects.toThrow('Username must contain')
    await expect(createFsAuthentication(['alice:tiny'], options)).rejects.toThrow('Password must contain 5-256 characters')
    await expect(createFsAuthentication(['Alice:password123', 'alice:password456'], options)).rejects.toThrow('specified more than once')
  })

  test('returns the same failure for an unknown username or wrong password', async () => {
    const authentication = (await createFsAuthentication(['alice:password123'], { directory: await sessionDirectory() }))!

    expect(await authentication.signIn({ username: 'alice', password: 'incorrect-password' })).toBeUndefined()
    expect(await authentication.signIn({ username: 'missing', password: 'password123' })).toBeUndefined()
    authentication.close()
  })

  test('expires sessions and notifies active observers', async () => {
    const authentication = (await createFsAuthentication(['alice:password123'], { directory: await sessionDirectory(), sessionLifetimeMs: 20 }))!
    const grant = (await authentication.signIn({ username: 'alice', password: 'password123' }))!
    let revoked = false
    authentication.observe(grant.token, () => {
      revoked = true
    })

    await Bun.sleep(40)

    expect(authentication.resume(grant.token)).toBeUndefined()
    expect(revoked).toBe(true)
    authentication.close()
  })

  test('evicts least-recently-used sessions at account and process limits', async () => {
    const authentication = (await createFsAuthentication(
      ['alice:password123', 'bob:password456'],
      { directory: await sessionDirectory(), maxAccountSessions: 2, maxSessions: 3 },
    ))!
    const aliceFirst = (await authentication.signIn({ username: 'alice', password: 'password123' }))!
    const aliceSecond = (await authentication.signIn({ username: 'alice', password: 'password123' }))!
    authentication.resume(aliceFirst.token)
    const aliceThird = (await authentication.signIn({ username: 'alice', password: 'password123' }))!

    expect(authentication.resume(aliceSecond.token)).toBeUndefined()
    expect(authentication.resume(aliceFirst.token)).toBeDefined()
    expect(authentication.resume(aliceThird.token)).toBeDefined()

    const bobFirst = (await authentication.signIn({ username: 'bob', password: 'password456' }))!
    const bobSecond = (await authentication.signIn({ username: 'bob', password: 'password456' }))!

    expect(authentication.resume(aliceFirst.token)).toBeUndefined()
    expect(authentication.resume(aliceThird.token)).toBeDefined()
    expect(authentication.resume(bobFirst.token)).toBeDefined()
    expect(authentication.resume(bobSecond.token)).toBeDefined()
    authentication.close()
  })

  test('restores an unchanged configured account after restart and rejects a password change', async () => {
    const directory = await sessionDirectory()
    const first = (await createFsAuthentication(['alice:password123'], { directory }))!
    const grant = (await first.signIn({ username: 'alice', password: 'password123' }))!
    first.close()

    const restored = (await createFsAuthentication(['alice:password123'], { directory }))!
    expect(restored.resume(grant.token)?.account).toEqual({ username: 'alice' })
    restored.close()

    const changed = (await createFsAuthentication(['alice:replacement-password'], { directory }))!
    expect(changed.resume(grant.token)).toBeUndefined()
    changed.close()
  })
})
