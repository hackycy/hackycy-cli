import type {
  ChangeStats,
  ContentPolicy,
  DiffHunk,
  FileChange,
  FileRole,
  GitChangeSnapshot,
  GitScope,
  SnapshotFile,
} from './types'
import { createHash } from 'node:crypto'
import { lstat } from 'node:fs/promises'
import path from 'node:path'
import { CommitMessageError } from './types'

const LARGE_FILE_BYTES = 200_000

const BINARY_EXTS = new Set([
  '.png',
  '.jpg',
  '.jpeg',
  '.gif',
  '.webp',
  '.ico',
  '.pdf',
  '.zip',
  '.gz',
  '.tgz',
  '.7z',
  '.rar',
  '.sqlite',
  '.db',
  '.woff',
  '.woff2',
  '.ttf',
  '.otf',
  '.mp3',
  '.mp4',
  '.mov',
])

const LOCKFILE_BASENAMES = new Set([
  'bun.lock',
  'bun.lockb',
  'package-lock.json',
  'pnpm-lock.yaml',
  'yarn.lock',
])

interface ParsedPatchFile {
  hunks: DiffHunk[]
  rawLength: number
}

interface Numstat {
  additions: number
  deletions: number
  binary: boolean
}

interface InspectionOptions {
  cwd?: string
  repoRoot?: string
  scope?: GitScope
}

interface CaptureOptions {
  repoRoot: string
  scope: GitScope
}

async function pathExists(filePath: string): Promise<boolean> {
  try {
    await lstat(filePath)
    return true
  }
  catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT')
      return false
    throw err
  }
}

async function runGit(args: string[], cwd?: string, allowFailure = false): Promise<string> {
  const proc = Bun.spawn(['git', ...args], {
    cwd,
    stdout: 'pipe',
    stderr: 'pipe',
  })
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ])

  if (exitCode !== 0 && !allowFailure)
    throw new Error(stderr.trim() || `git ${args.join(' ')} failed with exit code ${exitCode}`)

  return stdout
}

async function runGitBytes(args: string[], cwd: string, input: string): Promise<Uint8Array> {
  const proc = Bun.spawn(['git', ...args], {
    cwd,
    stdin: new TextEncoder().encode(input),
    stdout: 'pipe',
    stderr: 'pipe',
  })
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).arrayBuffer(),
    new Response(proc.stderr).text(),
    proc.exited,
  ])
  if (exitCode !== 0)
    throw new Error(stderr.trim() || `git ${args.join(' ')} failed with exit code ${exitCode}`)
  return new Uint8Array(stdout)
}

export async function getRepoRoot(cwd?: string): Promise<string> {
  return (await runGit(['rev-parse', '--show-toplevel'], cwd)).trim()
}

export async function stageAllChanges(repoRoot: string): Promise<void> {
  await runGit(['add', '-A'], repoRoot)
}

export async function hasStagedChanges(repoRoot: string): Promise<boolean> {
  const proc = Bun.spawn(['git', 'diff', '--cached', '--quiet'], { cwd: repoRoot })
  return (await proc.exited) === 1
}

export async function stageFiles(repoRoot: string, filePaths: string[]): Promise<void> {
  const existingPaths: string[] = []
  const missingPaths: string[] = []

  for (const filePath of filePaths) {
    if (await pathExists(path.join(repoRoot, filePath)))
      existingPaths.push(filePath)
    else
      missingPaths.push(filePath)
  }

  if (existingPaths.length > 0)
    await runGit(['add', '-A', '--', ...existingPaths], repoRoot)
  if (missingPaths.length > 0)
    await runGit(['update-index', '--remove', '--', ...missingPaths], repoRoot)
}

export async function unstageFiles(repoRoot: string, filePaths: string[]): Promise<void> {
  await runGit(['restore', '--staged', '--', ...filePaths], repoRoot)
}

export async function commitWithMessage(repoRoot: string, message: string): Promise<void> {
  const parts = message.split('\n').map(line => line.trimEnd())
  const subject = parts[0] ?? message
  const body = parts.slice(1).join('\n').trim()
  const args = ['commit', '-m', subject]
  if (body)
    args.push('-m', body)
  await runGit(args, repoRoot)
}

async function getCurrentBranch(repoRoot: string): Promise<string> {
  const branch = (await runGit(['branch', '--show-current'], repoRoot)).trim()
  if (!branch)
    throw new Error('Cannot push from detached HEAD. Check out a branch first.')
  return branch
}

export async function pushChanges(repoRoot: string, remote = 'origin'): Promise<void> {
  await runGit(['push', '-u', remote, await getCurrentBranch(repoRoot)], repoRoot)
}

function formatStatus(indexStatus: string, worktreeStatus: string, filePath: string, originalPath?: string): string {
  if (indexStatus === '?' && worktreeStatus === '?')
    return `A ${filePath}`
  if (indexStatus === 'R' || indexStatus === 'C')
    return `${indexStatus} ${originalPath ?? filePath} -> ${filePath}`
  const status = `${indexStatus}${worktreeStatus}`.trim()
  return `${status || 'M'} ${filePath}`
}

function parseStatus(output: string): FileChange[] {
  const chunks = output.split('\0').filter(Boolean)
  const files: FileChange[] = []

  for (let index = 0; index < chunks.length; index++) {
    const chunk = chunks[index]!
    const indexStatus = chunk[0] ?? ' '
    const worktreeStatus = chunk[1] ?? ' '
    const filePath = chunk.slice(3)
    const renamed = indexStatus === 'R' || indexStatus === 'C'
    const originalPath = renamed ? chunks[++index] : undefined
    files.push({
      path: filePath,
      originalPath,
      status: formatStatus(indexStatus, worktreeStatus, filePath, originalPath),
      indexStatus,
      worktreeStatus,
    })
  }

  return files
}

function parseSubmodulePaths(output: string): Set<string> {
  const paths = new Set<string>()
  for (const entry of output.split('\0').filter(Boolean)) {
    const tab = entry.indexOf('\t')
    if (tab === -1)
      continue
    if (entry.startsWith('160000 '))
      paths.add(entry.slice(tab + 1))
  }
  return paths
}

export async function inspectGitChanges(options: InspectionOptions = {}): Promise<{ repoRoot: string, files: FileChange[] }> {
  const repoRoot = options.repoRoot ?? await getRepoRoot(options.cwd)
  const [statusOutput, lsFilesOutput] = await Promise.all([
    runGit(['status', '--porcelain=v1', '-z', '--untracked-files=all'], repoRoot),
    runGit(['ls-files', '--stage', '-z'], repoRoot),
  ])
  const submodulePaths = parseSubmodulePaths(lsFilesOutput)
  const scope = options.scope ?? 'all-uncommitted'
  const files = parseStatus(statusOutput)
    .filter(file => scope === 'all-uncommitted' || (file.indexStatus !== ' ' && file.indexStatus !== '?'))
    .filter(file => !submodulePaths.has(file.path))
    .toSorted((left, right) => left.path.localeCompare(right.path))

  return { repoRoot, files }
}

function normalizePath(value: string): string {
  return value.replaceAll('\\', '/')
}

function isGeneratedPath(filePath: string): boolean {
  const normalized = normalizePath(filePath)
  const basename = path.basename(filePath)
  return LOCKFILE_BASENAMES.has(basename)
    || normalized.includes('/dist/')
    || normalized.startsWith('dist/')
    || normalized.includes('/build/')
    || normalized.startsWith('build/')
    || normalized.includes('/coverage/')
    || normalized.startsWith('coverage/')
    || basename.endsWith('.min.js')
    || basename.endsWith('.map')
}

function isSensitivePath(filePath: string): boolean {
  const basename = path.basename(filePath)
  const extension = path.extname(filePath).toLowerCase()
  return basename === '.env'
    || basename.startsWith('.env.')
    || basename === 'id_rsa'
    || basename === 'id_ed25519'
    || extension === '.pem'
    || extension === '.key'
    || extension === '.p12'
    || extension === '.pfx'
}

function isBinaryPath(filePath: string): boolean {
  return BINARY_EXTS.has(path.extname(filePath).toLowerCase())
}

export function getFileRole(filePath: string, binary = false): FileRole {
  const normalized = normalizePath(filePath)
  const basename = path.basename(filePath).toLowerCase()
  if (isSensitivePath(filePath))
    return 'sensitive'
  if (binary || isBinaryPath(filePath))
    return 'binary'
  if (isGeneratedPath(filePath))
    return 'generated'
  if (basename === 'package.json' || basename === 'composer.json' || basename === 'cargo.toml')
    return 'dependency'
  if (/(?:^|\/)(?:__tests__|test|tests|spec|specs)(?:\/|$)|\.(?:test|spec)\.[^.]+$/i.test(normalized))
    return 'test'
  if (basename.endsWith('.md') || basename.startsWith('readme') || normalized.startsWith('docs/'))
    return 'docs'
  if (normalized.startsWith('.github/') || /(?:^|\/)(?:[^/]+\.)?(?:config|rc)\.[^.]+$/i.test(normalized) || /\.(?:json|ya?ml|toml|ini)$/i.test(normalized))
    return 'config'
  if (/\.(?:[cm]?[jt]sx?|py|go|rs|java|kt|rb|php|swift|cs|c|cc|cpp|h|hpp)$/i.test(normalized))
    return 'source'
  return 'unknown'
}

async function getFileSize(repoRoot: string, filePath: string): Promise<number | undefined> {
  try {
    return (await lstat(path.join(repoRoot, filePath))).size
  }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT')
      return undefined
    throw error
  }
}

async function getContentPolicy(repoRoot: string, filePath: string, role: FileRole): Promise<ContentPolicy> {
  if (role === 'sensitive')
    return 'redacted'
  if (role === 'binary' || role === 'generated')
    return 'metadata-only'
  const size = await getFileSize(repoRoot, filePath)
  return size !== undefined && size > LARGE_FILE_BYTES ? 'metadata-only' : 'inspect'
}

function stripDiffPath(value: string): string | undefined {
  if (value === '/dev/null')
    return undefined
  const unquoted = value.replace(/^"|"$/g, '')
  return unquoted.replace(/^[ab]\//, '')
}

function parseHunkHeader(line: string): Omit<DiffHunk, 'id' | 'source' | 'addedLines' | 'deletedLines'> | undefined {
  const match = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?:\s(.*))?$/.exec(line)
  if (!match)
    return undefined
  return {
    oldStart: Number(match[1]),
    oldLines: Number(match[2] ?? 1),
    newStart: Number(match[3]),
    newLines: Number(match[4] ?? 1),
    heading: match[5]?.trim() || undefined,
  }
}

function parseUnifiedPatch(patch: string, source: DiffHunk['source']): Map<string, ParsedPatchFile> {
  const parsed = new Map<string, ParsedPatchFile>()
  for (const section of patch.split(/^diff --git /m).slice(1)) {
    const lines = section.split('\n')
    let oldPath: string | undefined
    let newPath: string | undefined
    for (const line of lines) {
      if (line.startsWith('--- '))
        oldPath = stripDiffPath(line.slice(4).split('\t', 1)[0]!)
      if (line.startsWith('+++ ')) {
        newPath = stripDiffPath(line.slice(4).split('\t', 1)[0]!)
        break
      }
    }
    const filePath = newPath ?? oldPath
    if (!filePath)
      continue

    const hunks: DiffHunk[] = []
    let current: DiffHunk | undefined
    for (const line of lines) {
      const header = parseHunkHeader(line)
      if (header) {
        current = {
          ...header,
          id: `${source}:${filePath}:${hunks.length}`,
          source,
          addedLines: [],
          deletedLines: [],
        }
        hunks.push(current)
        continue
      }
      if (!current)
        continue
      if (line.startsWith('+') && !line.startsWith('+++'))
        current.addedLines.push(line.slice(1))
      else if (line.startsWith('-') && !line.startsWith('---'))
        current.deletedLines.push(line.slice(1))
    }
    parsed.set(filePath, { hunks, rawLength: section.length + 'diff --git '.length })
  }
  return parsed
}

function parseNumstat(output: string): Map<string, Numstat> {
  const stats = new Map<string, Numstat>()
  const chunks = output.split('\0').filter(Boolean)
  for (let index = 0; index < chunks.length; index++) {
    const chunk = chunks[index]!
    const match = /^([^\t]+)\t([^\t]+)\t(.*)$/.exec(chunk)
    if (!match)
      continue
    let filePath = match[3]
    if (!filePath) {
      index += 1
      const originalPath = chunks[index]
      index += 1
      filePath = chunks[index] ?? originalPath ?? ''
    }
    if (!filePath)
      continue
    const additions = match[1] === '-' ? 0 : Number(match[1])
    const deletions = match[2] === '-' ? 0 : Number(match[2])
    const existing = stats.get(filePath) ?? { additions: 0, deletions: 0, binary: false }
    existing.additions += Number.isFinite(additions) ? additions : 0
    existing.deletions += Number.isFinite(deletions) ? deletions : 0
    existing.binary ||= match[1] === '-' || match[2] === '-'
    stats.set(filePath, existing)
  }
  return stats
}

function mergeStats(...maps: Array<Map<string, Numstat>>): Map<string, Numstat> {
  const merged = new Map<string, Numstat>()
  for (const map of maps) {
    for (const [filePath, stat] of map) {
      const current = merged.get(filePath) ?? { additions: 0, deletions: 0, binary: false }
      current.additions += stat.additions
      current.deletions += stat.deletions
      current.binary ||= stat.binary
      merged.set(filePath, current)
    }
  }
  return merged
}

function untrackedPatch(filePath: string, text: string): string {
  const lines = text.endsWith('\n') ? text.slice(0, -1).split('\n') : text.split('\n')
  return [
    `diff --git a/${filePath} b/${filePath}`,
    'new file mode 100644',
    '--- /dev/null',
    `+++ b/${filePath}`,
    `@@ -0,0 +1,${lines.length} @@`,
    ...lines.map(line => `+${line}`),
  ].join('\n')
}

async function hashFile(filePath: string): Promise<string> {
  const hash = createHash('sha256')
  const reader = Bun.file(filePath).stream().getReader()
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done)
        break
      hash.update(value)
    }
  }
  finally {
    reader.releaseLock()
  }
  return hash.digest('hex')
}

async function mapWithConcurrency<T, R>(items: T[], limit: number, mapper: (item: T) => Promise<R>): Promise<R[]> {
  const result = [] as R[]
  result.length = items.length
  let next = 0
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (next < items.length) {
      const index = next++
      result[index] = await mapper(items[index]!)
    }
  })
  await Promise.all(workers)
  return result
}

async function captureUntrackedContents(repoRoot: string, files: FileChange[]): Promise<Map<string, { patch?: string, hash: string, binary: boolean }>> {
  const untracked = files.filter(file => file.indexStatus === '?' && file.worktreeStatus === '?')
  const captured = await mapWithConcurrency(untracked, 8, async (file) => {
    const absolutePath = path.join(repoRoot, file.path)
    const role = getFileRole(file.path)
    const policy = await getContentPolicy(repoRoot, file.path, role)
    const hash = await hashFile(absolutePath)
    if (policy !== 'inspect')
      return [file.path, { hash, binary: role === 'binary' }] as const
    const text = await Bun.file(absolutePath).text()
    if (text.includes('\0'))
      return [file.path, { hash, binary: true }] as const
    return [file.path, { hash, patch: untrackedPatch(file.path, text), binary: false }] as const
  })
  return new Map(captured)
}

async function readWorkingText(repoRoot: string, filePath: string): Promise<string | undefined> {
  try {
    return await Bun.file(path.join(repoRoot, filePath)).text()
  }
  catch {
    return undefined
  }
}

async function readGitBlobs(repoRoot: string, specs: string[]): Promise<Map<string, string | undefined>> {
  if (specs.length === 0)
    return new Map()
  const bytes = await runGitBytes(['cat-file', '--batch'], repoRoot, `${specs.join('\n')}\n`)
  const decoder = new TextDecoder()
  const blobs = new Map<string, string | undefined>()
  let offset = 0
  for (const spec of specs) {
    const headerEnd = bytes.indexOf(10, offset)
    if (headerEnd === -1)
      throw new Error(`git cat-file returned an incomplete response for ${spec}`)
    const header = decoder.decode(bytes.subarray(offset, headerEnd))
    offset = headerEnd + 1
    if (header.endsWith(' missing')) {
      blobs.set(spec, undefined)
      continue
    }
    const match = /^[0-9a-f]+ blob (\d+)$/.exec(header)
    if (!match)
      throw new Error(`git cat-file returned an unexpected response for ${spec}: ${header}`)
    const length = Number(match[1])
    const content = bytes.subarray(offset, offset + length)
    if (content.length !== length || bytes[offset + length] !== 10)
      throw new Error(`git cat-file returned an incomplete blob for ${spec}`)
    blobs.set(spec, decoder.decode(content))
    offset += length + 1
  }
  return blobs
}

async function captureManifestStates(
  repoRoot: string,
  scope: GitScope,
  files: FileChange[],
): Promise<Map<string, SnapshotFile['manifest']>> {
  const manifests = files.filter(file => path.basename(file.path) === 'package.json')
  const beforeSpecs = manifests.map(file => `HEAD:${file.originalPath ?? file.path}`)
  const afterSpecs = scope === 'staged' ? manifests.map(file => `:${file.path}`) : []
  const blobs = await readGitBlobs(repoRoot, [...beforeSpecs, ...afterSpecs])
  const states = await Promise.all(manifests.map(async (file) => {
    const before = blobs.get(`HEAD:${file.originalPath ?? file.path}`)
    const after = scope === 'staged' ? blobs.get(`:${file.path}`) : await readWorkingText(repoRoot, file.path)
    return [file.path, { before, after }] as const
  }))
  return new Map(states)
}

function totalStats(files: SnapshotFile[]): ChangeStats {
  return files.reduce<ChangeStats>((total, file) => ({
    additions: total.additions + file.stats.additions,
    deletions: total.deletions + file.stats.deletions,
  }), { additions: 0, deletions: 0 })
}

function snapshotHash(
  scope: GitScope,
  files: FileChange[],
  cachedPatch: string,
  worktreePatch: string,
  untracked: Map<string, { hash: string }>,
): string {
  const status = files.map(file => [
    file.indexStatus,
    file.worktreeStatus,
    file.path,
    file.originalPath ?? '',
  ].join('\0')).join('\0')
  const untrackedHashes = [...untracked]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([filePath, value]) => `${filePath}\0${value.hash}`)
    .join('\0')
  return createHash('sha256')
    .update([scope, status, cachedPatch, worktreePatch, untrackedHashes].join('\0'))
    .digest('hex')
}

export async function captureGitSnapshot(options: CaptureOptions): Promise<GitChangeSnapshot> {
  try {
    const inspected = await inspectGitChanges({ repoRoot: options.repoRoot, scope: options.scope })
    const repoRoot = inspected.repoRoot
    const files = options.scope === 'staged'
      ? inspected.files.map(file => ({
          ...file,
          status: formatStatus(file.indexStatus, ' ', file.path, file.originalPath),
          worktreeStatus: ' ',
        }))
      : inspected.files
    const [cachedPatch, cachedNumstat, worktreePatch, worktreeNumstat, untracked] = await Promise.all([
      runGit(['diff', '--cached', '--no-ext-diff', '--find-renames', '--unified=0'], repoRoot),
      runGit(['diff', '--cached', '--numstat', '-z', '--find-renames'], repoRoot),
      options.scope === 'all-uncommitted'
        ? runGit(['diff', '--no-ext-diff', '--find-renames', '--unified=0'], repoRoot)
        : Promise.resolve(''),
      options.scope === 'all-uncommitted'
        ? runGit(['diff', '--numstat', '-z', '--find-renames'], repoRoot)
        : Promise.resolve(''),
      options.scope === 'all-uncommitted'
        ? captureUntrackedContents(repoRoot, files)
        : Promise.resolve(new Map<string, { patch?: string, hash: string, binary: boolean }>()),
    ])
    const stagedPatches = parseUnifiedPatch(cachedPatch, 'staged')
    const worktreePatches = parseUnifiedPatch(worktreePatch, 'worktree')
    const untrackedPatches = new Map<string, ParsedPatchFile>()
    for (const [filePath, content] of untracked) {
      if (content.patch)
        untrackedPatches.set(filePath, parseUnifiedPatch(content.patch, 'untracked').get(filePath) ?? { hunks: [], rawLength: content.patch.length })
    }
    const stats = mergeStats(parseNumstat(cachedNumstat), parseNumstat(worktreeNumstat))
    for (const [filePath, content] of untracked) {
      const patch = untrackedPatches.get(filePath)
      if (patch) {
        const additions = patch.hunks.reduce((count, hunk) => count + hunk.addedLines.length, 0)
        stats.set(filePath, { additions, deletions: 0, binary: content.binary })
      }
      else if (content.binary) {
        stats.set(filePath, { additions: 0, deletions: 0, binary: true })
      }
    }
    const manifestStates = await captureManifestStates(repoRoot, options.scope, files)
    const snapshotFiles = await Promise.all(files.map(async (file) => {
      const fileStats = stats.get(file.path) ?? { additions: 0, deletions: 0, binary: false }
      const patches = [stagedPatches.get(file.path), worktreePatches.get(file.path), untrackedPatches.get(file.path)]
        .filter((value): value is ParsedPatchFile => Boolean(value))
      const role = getFileRole(file.path, fileStats.binary)
      let contentPolicy = await getContentPolicy(repoRoot, file.path, role)
      if (patches.some(patch => patch.rawLength > LARGE_FILE_BYTES) && contentPolicy === 'inspect')
        contentPolicy = 'metadata-only'
      const hunks = contentPolicy === 'inspect'
        ? patches.flatMap(patch => patch.hunks)
        : []
      return {
        id: createHash('sha256').update([file.path, file.originalPath ?? '', file.indexStatus, file.worktreeStatus].join('\0')).digest('hex'),
        path: file.path,
        originalPath: file.originalPath,
        status: file.status,
        indexStatus: file.indexStatus,
        worktreeStatus: file.worktreeStatus,
        role,
        contentPolicy,
        stats: { additions: fileStats.additions, deletions: fileStats.deletions },
        hunks,
        manifest: manifestStates.get(file.path),
      } satisfies SnapshotFile
    }))
    const sortedFiles = snapshotFiles.toSorted((left, right) => left.path.localeCompare(right.path))
    return {
      repoRoot,
      scope: options.scope,
      snapshotId: snapshotHash(options.scope, files, cachedPatch, worktreePatch, untracked),
      files: sortedFiles,
      totals: totalStats(sortedFiles),
    }
  }
  catch (error) {
    if (error instanceof CommitMessageError)
      throw error
    throw new CommitMessageError('GIT_CAPTURE_FAILED', `Unable to capture Git snapshot: ${(error as Error).message}`, error)
  }
}

export async function assertGitSnapshotCurrent(
  repoRoot: string,
  scope: GitScope,
  expectedSnapshotId: string,
): Promise<void> {
  const current = await captureGitSnapshot({ repoRoot, scope })
  if (current.snapshotId !== expectedSnapshotId)
    throw new CommitMessageError('STALE_GIT_SCOPE', 'Git changes changed after the commit message was generated. Generate a new message before committing.')
}
