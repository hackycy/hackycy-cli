import type { UpdateTransaction } from './updater'
import fs from 'node:fs'
import { mkdir, mkdtemp, readFile, rm, utimes, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { version as currentVersion } from '../../../package.json'
import {
  applyUpdateTransaction,
  consumeUpdateState,
  createUpdateTransaction,
  getInternalUpdateArgs,
  getUpdateStatePath,
  readUpdateState,
  runInternalUpdater,
  waitForProcessExit,
  writeUpdateState,
} from './updater'

let directory: string

beforeEach(async () => {
  directory = await mkdtemp(path.join(os.tmpdir(), 'ycy-updater-'))
})

afterEach(async () => {
  await rm(directory, { recursive: true, force: true })
})

async function sha256(value: string | ArrayBuffer): Promise<string> {
  const input = typeof value === 'string' ? new TextEncoder().encode(value) : value
  const digest = await crypto.subtle.digest('SHA-256', input)
  return Array.from(new Uint8Array(digest))
    .map(byte => byte.toString(16).padStart(2, '0'))
    .join('')
}

function transaction(expectedHash: string, transactionId = 'test-transaction'): UpdateTransaction {
  const targetPath = path.join(directory, 'ycy.exe')

  return createUpdateTransaction({
    transactionId,
    parentPid: 2_147_483_647,
    targetPath,
    stagedPath: `${targetPath}.new.${transactionId}`,
    backupPath: `${targetPath}.backup.${transactionId}`,
    expectedHash,
    expectedVersion: '1.2.3',
    statePath: getUpdateStatePath(targetPath),
    updaterPath: path.join(os.tmpdir(), `ycy-updater-${crypto.randomUUID()}.exe`),
  })
}

describe('applyUpdateTransaction', () => {
  test('replaces the target and removes its backup after validation', async () => {
    const update = transaction(await sha256('new binary'))
    await writeFile(update.targetPath, 'old binary')
    await writeFile(update.stagedPath, 'new binary')

    await expect(applyUpdateTransaction(update, { verifyBinary: () => {} })).resolves.toBeUndefined()
    await expect(readFile(update.targetPath, 'utf8')).resolves.toBe('new binary')
    expect(fs.existsSync(update.stagedPath)).toBe(false)
    expect(fs.existsSync(update.backupPath)).toBe(false)
  })

  test('restores the original binary when installed checksum verification fails', async () => {
    const update = transaction('not-the-installed-hash')
    await writeFile(update.targetPath, 'old binary')
    await writeFile(update.stagedPath, 'new binary')

    await expect(applyUpdateTransaction(update, { verifyBinary: () => {} })).rejects.toThrow('Installed binary checksum verification failed.')
    await expect(readFile(update.targetPath, 'utf8')).resolves.toBe('old binary')
    expect(fs.existsSync(update.backupPath)).toBe(false)
  })

  test('reports a warning when the verified backup cannot be removed', async () => {
    const update = transaction(await sha256('new binary'))
    await writeFile(update.targetPath, 'old binary')
    await writeFile(update.stagedPath, 'new binary')

    const warning = await applyUpdateTransaction(update, {
      verifyBinary: () => {},
      removeFile(filePath) {
        if (filePath === update.backupPath) {
          const error = new Error('permission denied') as NodeJS.ErrnoException
          error.code = 'EIO'
          throw error
        }
        fs.unlinkSync(filePath)
      },
    })

    expect(warning).toBe('could not remove the previous binary: permission denied')
    await expect(readFile(update.targetPath, 'utf8')).resolves.toBe('new binary')
    expect(fs.existsSync(update.backupPath)).toBe(true)
  })
})

describe('update state', () => {
  test('cleans stale installer temporary files without touching active downloads', async () => {
    const targetPath = path.join(directory, 'ycy.exe')
    const stalePath = `${targetPath}.tmp.2147483647`
    const activePath = `${targetPath}.tmp.${process.pid}`
    await writeFile(stalePath, 'stale download')
    await writeFile(activePath, 'active download')

    expect(consumeUpdateState(targetPath)).toBeNull()
    expect(fs.existsSync(stalePath)).toBe(false)
    expect(fs.existsSync(activePath)).toBe(true)
  })

  test('cleans orphaned state temporary files but preserves pending transactions', async () => {
    const transactionId = '11111111-1111-4111-8111-111111111111'
    const update = transaction(await sha256('new binary'), transactionId)
    const orphanPath = `${update.statePath}.${update.transactionId}.tmp`
    await writeFile(orphanPath, JSON.stringify(update))

    expect(consumeUpdateState(update.targetPath)).toBeNull()
    expect(fs.existsSync(orphanPath)).toBe(false)

    const activeTransactionId = '22222222-2222-4222-8222-222222222222'
    const activeUpdate = transaction(await sha256('new binary'), activeTransactionId)
    activeUpdate.parentPid = process.pid
    const activePath = `${activeUpdate.statePath}.${activeUpdate.transactionId}.tmp`
    await writeFile(activePath, JSON.stringify(activeUpdate))

    expect(consumeUpdateState(activeUpdate.targetPath)).toBeNull()
    expect(fs.existsSync(activePath)).toBe(true)

    writeUpdateState(update)
    const pendingTransactionId = '33333333-3333-4333-8333-333333333333'
    const pendingUpdate = transaction(await sha256('new binary'), pendingTransactionId)
    const pendingTempPath = `${pendingUpdate.statePath}.${pendingUpdate.transactionId}.tmp`
    await writeFile(pendingTempPath, JSON.stringify(pendingUpdate))

    expect(consumeUpdateState(update.targetPath)).toMatchObject({ status: 'pending' })
    expect(fs.existsSync(pendingTempPath)).toBe(true)
  })

  test('removes malformed state temporary files after they become stale', async () => {
    const update = transaction(await sha256('new binary'), '44444444-4444-4444-8444-444444444444')
    const tempPath = `${update.statePath}.${update.transactionId}.tmp`
    await writeFile(tempPath, '{')
    const staleTime = new Date(Date.now() - 2 * 60_000)
    await utimes(tempPath, staleTime, staleTime)

    expect(consumeUpdateState(update.targetPath)).toBeNull()
    expect(fs.existsSync(tempPath)).toBe(false)
  })

  test('cleans a state temporary file when atomic state replacement fails', async () => {
    const update = transaction(await sha256('new binary'))
    await mkdir(update.statePath)

    expect(() => writeUpdateState(update)).toThrow()
    expect(fs.existsSync(`${update.statePath}.${update.transactionId}.tmp`)).toBe(false)
  })

  test('passes complete updater arguments and consumes completed state once', async () => {
    const update = transaction(await sha256('new binary'))
    update.status = 'succeeded'
    await writeFile(update.updaterPath, 'temporary updater')
    writeUpdateState(update)

    expect(getInternalUpdateArgs(update)).toEqual([
      '--internal-apply-update',
      '--transaction-id',
      'test-transaction',
      '--parent-pid',
      '2147483647',
      '--target-path',
      update.targetPath,
      '--staged-path',
      update.stagedPath,
      '--backup-path',
      update.backupPath,
      '--expected-hash',
      update.expectedHash,
      '--expected-version',
      '1.2.3',
      '--state-path',
      update.statePath,
    ])

    expect(consumeUpdateState(update.targetPath)).toMatchObject({ status: 'succeeded' })
    expect(fs.existsSync(update.statePath)).toBe(false)
    expect(fs.existsSync(update.updaterPath)).toBe(false)
  })

  test('keeps a pending update state for the CLI startup gate', async () => {
    const update = transaction(await sha256('new binary'))
    writeUpdateState(update)

    expect(consumeUpdateState(update.targetPath)).toMatchObject({ status: 'pending' })
    expect(readUpdateState(update.statePath)).toMatchObject({ status: 'pending' })
  })
})

test('waitForProcessExit polls until the parent process is gone', async () => {
  let checks = 0
  const sleeps: number[] = []

  await waitForProcessExit(
    123,
    () => ++checks < 3,
    async (milliseconds) => {
      sleeps.push(milliseconds)
    },
  )

  expect(checks).toBe(3)
  expect(sleeps).toEqual([50, 50])
})

test('runInternalUpdater records a rollback failure state', async () => {
  const update = transaction('not-the-installed-hash')
  const parent = Bun.spawn([process.execPath, '-e', ''], {
    stdin: 'ignore',
    stdout: 'ignore',
    stderr: 'ignore',
  })
  await parent.exited
  update.parentPid = parent.pid
  await writeFile(update.targetPath, 'old binary')
  await writeFile(update.stagedPath, 'new binary')
  writeUpdateState(update)

  await expect(runInternalUpdater(getInternalUpdateArgs(update).slice(1))).rejects.toThrow('Installed binary checksum verification failed.')

  expect(readUpdateState(update.statePath)).toMatchObject({
    status: 'failed',
    message: 'Installed binary checksum verification failed.',
  })
  await expect(readFile(update.targetPath, 'utf8')).resolves.toBe('old binary')
})

test.if(process.platform === 'win32' && process.env.YCY_RUN_COMPILED_UPDATER_TEST === '1')('applies an update from a compiled Windows updater', async () => {
  const updaterPath = path.join(directory, 'ycy-updater-compiled.exe')
  const build = Bun.spawn([
    process.execPath,
    'scripts/build.ts',
    '--target',
    'bun-windows-x64',
    '--outfile',
    updaterPath,
  ], {
    cwd: process.cwd(),
    stdin: 'ignore',
    stdout: 'ignore',
    stderr: 'ignore',
  })
  expect(await build.exited).toBe(0)

  const targetPath = path.join(directory, 'ycy.exe')
  const stagedPath = `${targetPath}.new.compiled-test`
  const backupPath = `${targetPath}.backup.compiled-test`
  fs.copyFileSync(updaterPath, targetPath)
  fs.copyFileSync(updaterPath, stagedPath)

  const parent = Bun.spawn([process.execPath, '-e', ''], {
    stdin: 'ignore',
    stdout: 'ignore',
    stderr: 'ignore',
  })
  await parent.exited

  const update = createUpdateTransaction({
    transactionId: 'compiled-test',
    parentPid: parent.pid,
    targetPath,
    stagedPath,
    backupPath,
    expectedHash: await sha256(await Bun.file(stagedPath).arrayBuffer()),
    expectedVersion: currentVersion,
    statePath: getUpdateStatePath(targetPath),
    updaterPath,
  })
  writeUpdateState(update)

  const updater = Bun.spawn([updaterPath, ...getInternalUpdateArgs(update)], {
    stdin: 'ignore',
    stdout: 'ignore',
    stderr: 'ignore',
    windowsHide: true,
  })
  expect(await updater.exited).toBe(0)
  expect(readUpdateState(update.statePath)).toMatchObject({ status: 'succeeded' })
  expect(fs.existsSync(backupPath)).toBe(false)

  const target = Bun.spawn([targetPath, '--version'], {
    stdin: 'ignore',
    stdout: 'pipe',
    stderr: 'pipe',
    windowsHide: true,
  })
  expect(await target.exited).toBe(0)
  const output = new TextDecoder().decode(await new Response(target.stdout).arrayBuffer())
  expect(output).toContain(`Updated ycy to v${currentVersion}.`)
  expect(output).toContain(currentVersion)
  expect(fs.existsSync(update.statePath)).toBe(false)
  expect(fs.existsSync(updaterPath)).toBe(false)
}, 120_000)
