import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'

export const INTERNAL_UPDATE_COMMAND = '--internal-apply-update'
export const INTERNAL_UPDATE_VERIFY_ENV = 'YCY_INTERNAL_UPDATE_VERIFY'

const PROCESS_POLL_INTERVAL_MS = 50
const PROCESS_EXIT_TIMEOUT_MS = 30_000
const FILE_OPERATION_RETRY_COUNT = 100
const STATE_TEMP_STALE_AFTER_MS = 60_000

export type UpdateStatus = 'pending' | 'succeeded' | 'succeeded_with_cleanup_warning' | 'failed'

export interface UpdateTransaction {
  transactionId: string
  parentPid: number
  targetPath: string
  stagedPath: string
  backupPath: string
  expectedHash: string
  expectedVersion: string
  statePath: string
  updaterPath: string
  createdAt: string
  status: UpdateStatus
  message?: string
}

export interface ApplyUpdateOptions {
  verifyBinary?: (filePath: string, expectedVersion: string) => void
  clearQuarantine?: (filePath: string) => void
  removeFile?: (filePath: string) => void
}

export function getUpdateStatePath(targetPath: string): string {
  return `${targetPath}.update-state.json`
}

export function createUpdateTransaction(options: Omit<UpdateTransaction, 'createdAt' | 'status'>): UpdateTransaction {
  return {
    ...options,
    createdAt: new Date().toISOString(),
    status: 'pending',
  }
}

export function writeUpdateState(state: UpdateTransaction): void {
  const tempPath = `${state.statePath}.${state.transactionId}.tmp`
  try {
    fs.writeFileSync(tempPath, JSON.stringify(state), 'utf8')
    fs.renameSync(tempPath, state.statePath)
  }
  finally {
    if (fs.existsSync(tempPath)) {
      tryRemoveFile(tempPath)
    }
  }
}

export function readUpdateState(statePath: string): UpdateTransaction | null {
  if (!fs.existsSync(statePath)) {
    return null
  }

  try {
    const value: unknown = JSON.parse(fs.readFileSync(statePath, 'utf8'))
    if (!isUpdateTransaction(value)) {
      return null
    }
    return value
  }
  catch {
    return null
  }
}

export function consumeUpdateState(targetPath: string): UpdateTransaction | null {
  const statePath = getUpdateStatePath(targetPath)
  const state = readUpdateState(statePath)
  cleanupTemporaryFiles(targetPath, state)

  if (!state || state.status === 'pending') {
    return state
  }

  tryRemoveUpdaterCopy(state.updaterPath)
  tryRemoveFile(statePath)
  return state
}

export function formatUpdateState(state: UpdateTransaction): string {
  if (state.status === 'succeeded') {
    return `Updated ycy to v${state.expectedVersion}.`
  }

  if (state.status === 'succeeded_with_cleanup_warning') {
    return `Updated ycy to v${state.expectedVersion}, but cleanup failed: ${state.message}`
  }

  if (state.status === 'failed') {
    return `Previous update failed and was rolled back: ${state.message}`
  }

  return 'An update is being applied. Retry in a moment.'
}

export function getInternalUpdateArgs(transaction: UpdateTransaction): string[] {
  return [
    INTERNAL_UPDATE_COMMAND,
    '--transaction-id',
    transaction.transactionId,
    '--parent-pid',
    String(transaction.parentPid),
    '--target-path',
    transaction.targetPath,
    '--staged-path',
    transaction.stagedPath,
    '--backup-path',
    transaction.backupPath,
    '--expected-hash',
    transaction.expectedHash,
    '--expected-version',
    transaction.expectedVersion,
    '--state-path',
    transaction.statePath,
  ]
}

export async function runInternalUpdater(args: string[]): Promise<void> {
  let transaction: UpdateTransaction | null = null

  try {
    transaction = parseInternalUpdateArgs(args)
    const storedState = readUpdateState(transaction.statePath)
    if (storedState?.transactionId === transaction.transactionId) {
      transaction = storedState
    }

    await waitForProcessExit(transaction.parentPid)
    const cleanupWarning = await applyUpdateTransaction(transaction)
    writeUpdateState({
      ...transaction,
      status: cleanupWarning ? 'succeeded_with_cleanup_warning' : 'succeeded',
      message: cleanupWarning,
    })
  }
  catch (error) {
    if (transaction) {
      tryRemoveFile(transaction.stagedPath)
      writeFailureState(transaction, error)
    }
    throw error
  }
  finally {
    if (transaction && process.platform !== 'win32' && path.resolve(process.execPath) === path.resolve(transaction.updaterPath)) {
      tryRemoveUpdaterCopy(transaction.updaterPath)
    }
  }
}

export async function waitForProcessExit(
  pid: number,
  isRunning: (pid: number) => boolean = isProcessRunning,
  sleep: (milliseconds: number) => Promise<void> = Bun.sleep,
): Promise<void> {
  const deadline = Date.now() + PROCESS_EXIT_TIMEOUT_MS

  while (isRunning(pid)) {
    if (Date.now() >= deadline) {
      throw new Error(`Timed out waiting for process ${pid} to exit.`)
    }
    await sleep(PROCESS_POLL_INTERVAL_MS)
  }
}

export async function applyUpdateTransaction(
  transaction: UpdateTransaction,
  options: ApplyUpdateOptions = {},
): Promise<string | undefined> {
  const verifyBinary = options.verifyBinary ?? verifyBinaryExecutable
  const clearQuarantine = options.clearQuarantine ?? clearQuarantineAttribute
  const removeFile = options.removeFile ?? fs.unlinkSync
  let originalMoved = false

  try {
    if (!fs.existsSync(transaction.stagedPath)) {
      throw new Error('Downloaded update file is missing.')
    }

    if (fs.existsSync(transaction.backupPath)) {
      throw new Error('A previous update backup is still present.')
    }

    if (fs.existsSync(transaction.targetPath)) {
      await retryFileOperation(() => fs.renameSync(transaction.targetPath, transaction.backupPath))
      originalMoved = true
    }

    await retryFileOperation(() => fs.renameSync(transaction.stagedPath, transaction.targetPath))

    if (process.platform !== 'win32') {
      fs.chmodSync(transaction.targetPath, 0o755)
    }
    clearQuarantine(transaction.targetPath)

    const installedHash = await sha256File(transaction.targetPath)
    if (installedHash !== transaction.expectedHash) {
      throw new Error('Installed binary checksum verification failed.')
    }
    verifyBinary(transaction.targetPath, transaction.expectedVersion)
  }
  catch (error) {
    if (originalMoved) {
      try {
        if (fs.existsSync(transaction.targetPath)) {
          await retryFileOperation(() => removeFile(transaction.targetPath))
        }
        if (fs.existsSync(transaction.backupPath)) {
          await retryFileOperation(() => fs.renameSync(transaction.backupPath, transaction.targetPath))
        }
      }
      catch (restoreError) {
        throw new Error(`${errorMessage(error)} Rollback failed: ${errorMessage(restoreError)}`)
      }
    }
    throw error
  }

  if (!originalMoved || !fs.existsSync(transaction.backupPath)) {
    return undefined
  }

  try {
    await retryFileOperation(() => removeFile(transaction.backupPath))
    return undefined
  }
  catch (error) {
    return `could not remove the previous binary: ${errorMessage(error)}`
  }
}

function parseInternalUpdateArgs(args: string[]): UpdateTransaction {
  const values = new Map<string, string>()

  for (let index = 0; index < args.length; index += 2) {
    const name = args[index]
    const value = args[index + 1]
    if (!name?.startsWith('--') || !value || values.has(name)) {
      throw new Error('Invalid internal updater arguments.')
    }
    values.set(name, value)
  }

  const parentPid = Number(values.get('--parent-pid'))
  if (!Number.isSafeInteger(parentPid) || parentPid <= 0) {
    throw new Error('Invalid internal updater parent process ID.')
  }

  const transactionId = readArgument(values, '--transaction-id')
  const targetPath = readArgument(values, '--target-path')
  const stagedPath = readArgument(values, '--staged-path')
  const backupPath = readArgument(values, '--backup-path')
  const expectedHash = readArgument(values, '--expected-hash')
  const expectedVersion = readArgument(values, '--expected-version')
  const statePath = readArgument(values, '--state-path')

  if (path.dirname(targetPath) !== path.dirname(stagedPath) || path.dirname(targetPath) !== path.dirname(backupPath)) {
    throw new Error('Update files must be in the target binary directory.')
  }

  return {
    transactionId,
    parentPid,
    targetPath,
    stagedPath,
    backupPath,
    expectedHash,
    expectedVersion,
    statePath,
    updaterPath: process.execPath,
    createdAt: new Date().toISOString(),
    status: 'pending',
  }
}

function readArgument(values: Map<string, string>, name: string): string {
  const value = values.get(name)
  if (!value) {
    throw new Error(`Missing internal updater argument: ${name}`)
  }
  return value
}

function isUpdateTransaction(value: unknown): value is UpdateTransaction {
  if (!value || typeof value !== 'object') {
    return false
  }

  const state = value as Partial<UpdateTransaction>
  return typeof state.transactionId === 'string'
    && typeof state.parentPid === 'number'
    && typeof state.targetPath === 'string'
    && typeof state.stagedPath === 'string'
    && typeof state.backupPath === 'string'
    && typeof state.expectedHash === 'string'
    && typeof state.expectedVersion === 'string'
    && typeof state.statePath === 'string'
    && typeof state.updaterPath === 'string'
    && typeof state.createdAt === 'string'
    && (state.status === 'pending' || state.status === 'succeeded' || state.status === 'succeeded_with_cleanup_warning' || state.status === 'failed')
    && (state.message === undefined || typeof state.message === 'string')
}

function isProcessRunning(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  }
  catch (error) {
    return (error as NodeJS.ErrnoException).code !== 'ESRCH'
  }
}

function cleanupTemporaryFiles(targetPath: string, state: UpdateTransaction | null): void {
  const targetDirectory = path.dirname(targetPath)
  let entries: fs.Dirent[]

  try {
    entries = fs.readdirSync(targetDirectory, { withFileTypes: true })
  }
  catch {
    return
  }

  const targetName = path.basename(targetPath)
  const statePath = getUpdateStatePath(targetPath)
  const stateFileName = path.basename(statePath)
  const installerTempPattern = new RegExp(`^${escapeRegExp(targetName)}\\.tmp\\.(\\d+)$`)
  const stateTempPattern = new RegExp(`^${escapeRegExp(stateFileName)}\\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\\.tmp$`, 'i')
  const now = Date.now()

  for (const entry of entries) {
    if (!entry.isFile()) {
      continue
    }

    const entryPath = path.join(targetDirectory, entry.name)
    const installerMatch = entry.name.match(installerTempPattern)
    if (installerMatch) {
      const pid = Number(installerMatch[1])
      if (Number.isSafeInteger(pid) && pid > 0 && !isProcessRunning(pid)) {
        tryRemoveFile(entryPath)
      }
      continue
    }

    if (!stateTempPattern.test(entry.name)) {
      continue
    }

    if (state?.status === 'pending') {
      continue
    }

    const tempState = readUpdateState(entryPath)
    if (tempState
      && entry.name === `${stateFileName}.${tempState.transactionId}.tmp`
      && path.resolve(tempState.targetPath) === path.resolve(targetPath)
      && path.resolve(tempState.statePath) === path.resolve(statePath)) {
      if (!isProcessRunning(tempState.parentPid)) {
        tryRemoveFile(entryPath)
      }
      continue
    }

    try {
      if (now - fs.statSync(entryPath).mtimeMs >= STATE_TEMP_STALE_AFTER_MS) {
        tryRemoveFile(entryPath)
      }
    }
    catch {
      // Cleanup is best effort and must not block normal CLI startup.
    }
  }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

async function retryFileOperation(operation: () => void): Promise<void> {
  let lastError: unknown

  for (let attempt = 0; attempt < FILE_OPERATION_RETRY_COUNT; attempt += 1) {
    try {
      operation()
      return
    }
    catch (error) {
      lastError = error
      if (!isRetryableFileError(error) || attempt === FILE_OPERATION_RETRY_COUNT - 1) {
        throw error
      }
      await Bun.sleep(PROCESS_POLL_INTERVAL_MS)
    }
  }

  throw lastError
}

function isRetryableFileError(error: unknown): boolean {
  const code = (error as NodeJS.ErrnoException).code
  return code === 'EACCES' || code === 'EBUSY' || code === 'EPERM'
}

async function sha256File(filePath: string): Promise<string> {
  const data = await Bun.file(filePath).arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', data)
  return Array.from(new Uint8Array(digest))
    .map(byte => byte.toString(16).padStart(2, '0'))
    .join('')
}

function clearQuarantineAttribute(filePath: string): void {
  if (process.platform !== 'darwin') {
    return
  }

  const result = Bun.spawnSync(['xattr', '-d', 'com.apple.quarantine', filePath], {
    stdout: 'ignore',
    stderr: 'ignore',
  })

  if (result.exitCode !== 0 && result.exitCode !== 1) {
    throw new Error('Failed to clear macOS quarantine attribute.')
  }
}

function verifyBinaryExecutable(filePath: string, expectedVersion: string): void {
  const result = Bun.spawnSync([filePath, '--version'], {
    env: {
      ...process.env,
      [INTERNAL_UPDATE_VERIFY_ENV]: '1',
    },
    stdout: 'pipe',
    stderr: 'pipe',
  })

  if (result.exitCode !== 0) {
    throw new Error(decodeOutput(result.stderr) || 'Installed binary failed to execute self-check.')
  }

  const actualVersion = decodeOutput(result.stdout)
  if (actualVersion !== expectedVersion && !actualVersion.startsWith(`ycy/${expectedVersion}`)) {
    throw new Error(`Installed binary reported unexpected version: ${actualVersion || '<empty>'}`)
  }
}

function decodeOutput(output: Uint8Array<ArrayBufferLike> | undefined): string {
  return output ? new TextDecoder().decode(output).trim() : ''
}

function writeFailureState(transaction: UpdateTransaction, error: unknown): void {
  try {
    writeUpdateState({
      ...transaction,
      status: 'failed',
      message: errorMessage(error),
    })
  }
  catch {
    // The updater cannot report to the detached parent when its state file is unavailable.
  }
}

function tryRemoveFile(filePath: string): void {
  try {
    fs.unlinkSync(filePath)
  }
  catch {
    // Cleanup is retried on a future normal CLI invocation where applicable.
  }
}

function tryRemoveUpdaterCopy(filePath: string): void {
  const relativePath = path.relative(path.resolve(os.tmpdir()), path.resolve(filePath))
  if (!relativePath || relativePath.startsWith('..') || path.isAbsolute(relativePath) || !path.basename(filePath).startsWith('ycy-updater-')) {
    return
  }
  tryRemoveFile(filePath)
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
