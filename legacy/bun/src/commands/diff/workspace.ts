import type { Dirent } from 'node:fs'
import type {
  BlobContent,
  ComparisonEntry,
  ComparisonEntryKind,
  ComparisonEntryState,
  ComparisonListEntry,
  ComparisonSide,
  ComparisonSnapshot,
  ComparisonStatus,
  ComparisonWorkspace,
  ComparisonWorkspaceOptions,
  EntryPresentation,
  RefreshRun,
  StatusCounts,
  TextContent,
  TextDiffResult,
  TextEncoding,
  TreeNode,
  WorkspaceProgress,
  WorkspaceState,
} from './types'
import { Buffer } from 'node:buffer'
import { constants } from 'node:fs'
import { lstat, open, readdir, readFile, readlink, realpath } from 'node:fs/promises'
import path from 'node:path'
import { FILE_HEADERS_ONLY, formatPatch, structuredPatch } from 'diff'
import ignore from 'ignore'

interface DiscoveredEntry {
  kind: ComparisonEntryKind
  size: number
  linkTarget?: string
  device: number
  inode: number
  modifiedAt: number
  changedAt: number
}

interface DiscoveryResult {
  entries: Map<string, DiscoveredEntry>
  issues: Map<string, string>
}

interface DirectoryIdentity {
  device: number
  inode: number
}

interface IgnoreDiscovery {
  matchers: Map<string, IgnoreMatcher>
  issues: Map<string, string>
  blockedDirectories: Set<string>
}

const MAX_CONFIRMED_TEXT_BYTES = 20 * 1024 * 1024
const MAX_AUTO_TEXT_BYTES = 2 * 1024 * 1024
const MAX_AUTO_TEXT_LINES = 50_000
const MAX_CONFIRMED_TEXT_LINES = 200_000
const MAX_TEXT_LINE_LENGTH = 1024 * 1024
const IMAGE_EXTENSIONS = new Set(['.avif', '.gif', '.jpeg', '.jpg', '.png', '.svg', '.webp'])
const IMAGE_MIME_TYPES = new Map([
  ['.avif', 'image/avif'],
  ['.gif', 'image/gif'],
  ['.jpeg', 'image/jpeg'],
  ['.jpg', 'image/jpeg'],
  ['.png', 'image/png'],
  ['.svg', 'image/svg+xml'],
  ['.webp', 'image/webp'],
])

interface DecodedText {
  text: string
  encoding: TextEncoding
}

function decodeText(bytes: Uint8Array): DecodedText | undefined {
  try {
    if (bytes.length >= 2 && bytes[0] === 0xFF && bytes[1] === 0xFE)
      return { text: new TextDecoder('utf-16le', { fatal: true }).decode(bytes), encoding: 'utf-16le' }
    if (bytes.length >= 2 && bytes[0] === 0xFE && bytes[1] === 0xFF)
      return { text: new TextDecoder('utf-16be', { fatal: true }).decode(bytes), encoding: 'utf-16be' }
    return { text: new TextDecoder('utf-8', { fatal: true }).decode(bytes), encoding: 'utf-8' }
  }
  catch {
    return undefined
  }
}

async function loadTextContent(
  side: DiscoveredEntry | undefined,
  absolutePath: string,
  force: boolean,
): Promise<TextContent> {
  if (!side)
    return { status: 'missing' }
  if (side.kind !== 'file')
    return { status: 'binary' }
  if (side.size > MAX_CONFIRMED_TEXT_BYTES)
    return { status: 'blocked', size: side.size }

  const bytes = await readStableBytes(side, absolutePath)
  if (bytes === 'stale')
    return { status: 'stale' }
  const decoded = decodeText(bytes)
  if (!decoded)
    return { status: 'binary' }

  let lineCount = decoded.text.length === 0 ? 0 : 1
  let lineLength = 0
  let hasOversizedLine = false
  for (let index = 0; index < decoded.text.length; index++) {
    const character = decoded.text.charCodeAt(index)
    if (character === 10 || character === 13) {
      lineCount++
      lineLength = 0
      if (character === 13 && decoded.text.charCodeAt(index + 1) === 10)
        index++
    }
    else if (++lineLength > MAX_TEXT_LINE_LENGTH) {
      hasOversizedLine = true
    }
  }
  if (lineCount > MAX_CONFIRMED_TEXT_LINES || hasOversizedLine)
    return { status: 'blocked', size: side.size, lineCount }
  if (!force && (side.size > MAX_AUTO_TEXT_BYTES || lineCount > MAX_AUTO_TEXT_LINES))
    return { status: 'guarded', size: side.size, lineCount }
  return { status: 'ready', ...decoded, size: side.size, lineCount }
}

function hasRecordedFingerprint(
  stat: Awaited<ReturnType<typeof lstat>>,
  entry: DiscoveredEntry,
): boolean {
  return stat.isFile()
    && stat.dev === entry.device
    && stat.ino === entry.inode
    && stat.size === entry.size
    && stat.mtimeMs === entry.modifiedAt
    && stat.ctimeMs === entry.changedAt
}

async function readStableBytes(entry: DiscoveredEntry, absolutePath: string): Promise<Uint8Array | 'stale'> {
  let handle: Awaited<ReturnType<typeof open>> | undefined
  try {
    handle = await open(absolutePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0))
    if (path.relative(absolutePath, await realpath(absolutePath)) !== '')
      return 'stale'
    const before = await handle.stat()
    if (!hasRecordedFingerprint(before, entry))
      return 'stale'
    const bytes = await handle.readFile()
    const after = await handle.stat()
    if (
      !hasRecordedFingerprint(after, entry)
      || path.relative(absolutePath, await realpath(absolutePath)) !== ''
    ) {
      return 'stale'
    }
    return bytes
  }
  catch {
    return 'stale'
  }
  finally {
    await handle?.close()
  }
}

async function classifyPresentation(
  entry: ComparisonEntry,
  baseline: DiscoveredEntry | undefined,
  target: DiscoveredEntry | undefined,
  baselinePath: string,
  targetPath: string,
): Promise<Exclude<EntryPresentation, 'issue'>> {
  if (baseline?.kind === 'symlink' || target?.kind === 'symlink')
    return 'symlink'
  if (IMAGE_EXTENSIONS.has(path.extname(entry.path).toLowerCase()))
    return 'image'
  if (Math.max(baseline?.size ?? 0, target?.size ?? 0) > MAX_CONFIRMED_TEXT_BYTES)
    return 'oversized'

  const sides: Array<[DiscoveredEntry, string]> = []
  if (baseline)
    sides.push([baseline, baselinePath])
  if (target)
    sides.push([target, targetPath])
  const contents = await Promise.all(sides.map(([side, absolutePath]) => readStableBytes(side, absolutePath)))
  if (contents.includes('stale'))
    return 'stale'
  return contents.every(content => decodeText(content as Uint8Array) !== undefined) ? 'text' : 'binary'
}

type IgnoreMatcher = ReturnType<typeof ignore>

const MAX_DIRECTORY_CONCURRENCY = 16

const WINDOWS_SYSTEM_FILES = new Set(['thumbs.db', 'ehthumbs.db', 'desktop.ini'])
const WINDOWS_SYSTEM_DIRECTORIES = new Set(['$recycle.bin', 'system volume information'])
const MACOS_SYSTEM_DIRECTORIES = new Set(['.Spotlight-V100', '.Trashes'])

function isHardExcluded(comparisonPath: string, directory: boolean): boolean {
  const name = comparisonPath.slice(comparisonPath.lastIndexOf('/') + 1)
  if (name === '.git')
    return true
  if (name === '.DS_Store' || name.startsWith('._'))
    return true
  if (directory && MACOS_SYSTEM_DIRECTORIES.has(name))
    return true

  const lowerName = name.toLowerCase()
  return directory
    ? WINDOWS_SYSTEM_DIRECTORIES.has(lowerName)
    : WINDOWS_SYSTEM_FILES.has(lowerName)
}

function isExplicitlyExcluded(comparisonPath: string, directory: boolean, exclusions: Bun.Glob[]): boolean {
  return exclusions.some(glob => glob.match(comparisonPath) || (directory && glob.match(`${comparisonPath}/`)))
}

function relativeToBase(comparisonPath: string, basePath: string): string | undefined {
  if (!basePath)
    return comparisonPath
  const prefix = `${basePath}/`
  return comparisonPath.startsWith(prefix) ? comparisonPath.slice(prefix.length) : undefined
}

function isGitIgnored(comparisonPath: string, directory: boolean, matchers: Map<string, IgnoreMatcher>): boolean {
  const directoryPath = comparisonPath.includes('/') ? comparisonPath.slice(0, comparisonPath.lastIndexOf('/')) : ''
  const ancestors = ['']
  if (directoryPath) {
    let current = ''
    for (const part of directoryPath.split('/')) {
      current = current ? `${current}/${part}` : part
      ancestors.push(current)
    }
  }

  let ignored = false
  for (const basePath of ancestors) {
    const matcher = matchers.get(basePath)
    const relativePath = relativeToBase(comparisonPath, basePath)
    if (!matcher || !relativePath)
      continue
    const result = matcher.test(directory ? `${relativePath}/` : relativePath)
    if (result.ignored)
      ignored = true
    else if (result.unignored)
      ignored = false
  }
  return ignored
}

async function mapConcurrent<Input, Output>(
  values: Input[],
  concurrency: number,
  transform: (value: Input) => Promise<Output>,
): Promise<Output[]> {
  const results: Output[] = []
  let nextIndex = 0
  const worker = async (): Promise<void> => {
    while (nextIndex < values.length) {
      const index = nextIndex++
      results[index] = await transform(values[index]!)
    }
  }
  await Promise.all(Array.from(
    { length: Math.min(concurrency, Math.max(values.length, 1)) },
    () => worker(),
  ))
  return results
}

function errorCode(error: unknown): string {
  return (error as NodeJS.ErrnoException).code ?? 'UNKNOWN'
}

function recordIssue(issues: Map<string, string>, comparisonPath: string, message: string): void {
  const existing = issues.get(comparisonPath)
  issues.set(comparisonPath, existing ? `${existing}; ${message}` : message)
}

function mergeIssues(target: Map<string, string>, source: Map<string, string>): void {
  for (const [comparisonPath, message] of source)
    recordIssue(target, comparisonPath, message)
}

function isBlocked(comparisonPath: string, blockedDirectories: Set<string>): boolean {
  if (blockedDirectories.has(''))
    return true
  let current = comparisonPath
  while (current) {
    if (blockedDirectories.has(current))
      return true
    const separator = current.lastIndexOf('/')
    current = separator === -1 ? '' : current.slice(0, separator)
  }
  return false
}

async function collectTargetIgnoreMatchers(
  root: string,
  exclusions: Bun.Glob[],
  signal: AbortSignal,
): Promise<IgnoreDiscovery> {
  const matchers = new Map<string, IgnoreMatcher>()
  const issues = new Map<string, string>()
  const blockedDirectories = new Set<string>()
  let directories = ['']

  while (directories.length > 0) {
    const discovered = await mapConcurrent(directories, MAX_DIRECTORY_CONCURRENCY, async (relativeDirectory) => {
      signal.throwIfAborted()
      const absoluteDirectory = path.join(root, ...relativeDirectory.split('/').filter(Boolean))
      try {
        const rules = await readFile(path.join(absoluteDirectory, '.gitignore'), 'utf8')
        matchers.set(relativeDirectory, ignore().add(rules))
      }
      catch (error) {
        const code = errorCode(error)
        if (code !== 'ENOENT' && code !== 'EISDIR') {
          const gitignorePath = relativeDirectory ? `${relativeDirectory}/.gitignore` : '.gitignore'
          recordIssue(issues, gitignorePath, `Target Directory ignore rules could not be read (${code})`)
          blockedDirectories.add(relativeDirectory)
          return []
        }
      }

      let children: Dirent<string>[]
      try {
        children = await readdir(absoluteDirectory, { withFileTypes: true })
      }
      catch (error) {
        const issuePath = relativeDirectory || '.'
        recordIssue(issues, issuePath, `Target Directory could not be read (${errorCode(error)})`)
        blockedDirectories.add(relativeDirectory)
        return []
      }

      return children.flatMap((child) => {
        if (!child.isDirectory())
          return []
        const relativePath = relativeDirectory ? `${relativeDirectory}/${child.name}` : child.name
        return !isHardExcluded(relativePath, true)
          && !isGitIgnored(relativePath, true, matchers)
          && !isExplicitlyExcluded(relativePath, true, exclusions)
          ? [relativePath]
          : []
      })
    })
    directories = discovered.flat()
  }

  return { matchers, issues, blockedDirectories }
}

async function discover(
  root: string,
  matchers: Map<string, IgnoreMatcher>,
  exclusions: Bun.Glob[],
  blockedDirectories: Set<string>,
  sideLabel: 'Baseline Directory' | 'Target Directory',
  signal: AbortSignal,
  onDiscovered: (issue: boolean) => void,
): Promise<DiscoveryResult> {
  const entries = new Map<string, DiscoveredEntry>()
  const issues = new Map<string, string>()
  let directories = isBlocked('', blockedDirectories) ? [] : ['']

  while (directories.length > 0) {
    const discovered = await mapConcurrent(directories, MAX_DIRECTORY_CONCURRENCY, async (relativeDirectory) => {
      signal.throwIfAborted()
      const absoluteDirectory = path.join(root, ...relativeDirectory.split('/').filter(Boolean))
      let children: Dirent<string>[]
      try {
        children = await readdir(absoluteDirectory, { withFileTypes: true })
      }
      catch (error) {
        const issuePath = relativeDirectory || '.'
        recordIssue(issues, issuePath, `${sideLabel} could not be read (${errorCode(error)})`)
        onDiscovered(true)
        return []
      }

      const childDirectories: string[] = []
      for (const child of children) {
        signal.throwIfAborted()
        const relativePath = relativeDirectory ? `${relativeDirectory}/${child.name}` : child.name
        if (isBlocked(relativePath, blockedDirectories))
          continue
        const directory = child.isDirectory()
        if (
          isHardExcluded(relativePath, directory)
          || isGitIgnored(relativePath, directory, matchers)
          || isExplicitlyExcluded(relativePath, directory, exclusions)
        ) {
          continue
        }

        const absolutePath = path.join(absoluteDirectory, child.name)
        let stat: Awaited<ReturnType<typeof lstat>>
        try {
          stat = await lstat(absolutePath)
        }
        catch (error) {
          recordIssue(issues, relativePath, `${sideLabel} entry could not be inspected (${errorCode(error)})`)
          onDiscovered(true)
          continue
        }

        if (stat.isDirectory()) {
          childDirectories.push(relativePath)
        }
        else if (stat.isFile()) {
          entries.set(relativePath, {
            kind: 'file',
            size: stat.size,
            device: stat.dev,
            inode: stat.ino,
            modifiedAt: stat.mtimeMs,
            changedAt: stat.ctimeMs,
          })
          onDiscovered(false)
        }
        else if (stat.isSymbolicLink()) {
          try {
            entries.set(relativePath, {
              kind: 'symlink',
              size: stat.size,
              linkTarget: await readlink(absolutePath),
              device: stat.dev,
              inode: stat.ino,
              modifiedAt: stat.mtimeMs,
              changedAt: stat.ctimeMs,
            })
            onDiscovered(false)
          }
          catch (error) {
            recordIssue(issues, relativePath, `${sideLabel} symbolic link could not be read (${errorCode(error)})`)
            onDiscovered(true)
          }
        }
        else {
          recordIssue(issues, relativePath, `${sideLabel} entry has an unsupported filesystem kind`)
          onDiscovered(true)
        }
      }
      return childDirectories
    })
    directories = discovered.flat()
  }

  return { entries, issues }
}

async function hasEqualContent(
  baseline: DiscoveredEntry,
  target: DiscoveredEntry,
  baselinePath: string,
  targetPath: string,
  signal: AbortSignal,
  onBytesCompared: (bytes: number) => void,
): Promise<boolean> {
  if (baseline.kind !== target.kind || baseline.size !== target.size)
    return false
  if (baseline.kind === 'symlink')
    return baseline.linkTarget === target.linkTarget

  const flags = constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0)
  const baselineHandle = await open(baselinePath, flags)
  let targetHandle: Awaited<ReturnType<typeof open>> | undefined
  try {
    targetHandle = await open(targetPath, flags)
    const [baselineBefore, targetBefore] = await Promise.all([
      baselineHandle.stat(),
      targetHandle.stat(),
    ])
    if (!hasRecordedFingerprint(baselineBefore, baseline) || !hasRecordedFingerprint(targetBefore, target))
      throw new Error('Comparison Entry changed while the snapshot was being built')

    const chunkSize = 64 * 1024
    const baselineBuffer = Buffer.allocUnsafe(chunkSize)
    const targetBuffer = Buffer.allocUnsafe(chunkSize)
    let equal = true
    for (let position = 0; position < baseline.size; position += chunkSize) {
      signal.throwIfAborted()
      const length = Math.min(chunkSize, baseline.size - position)
      const [baselineRead, targetRead] = await Promise.all([
        baselineHandle.read(baselineBuffer, 0, length, position),
        targetHandle.read(targetBuffer, 0, length, position),
      ])
      onBytesCompared(Math.max(baselineRead.bytesRead, targetRead.bytesRead))
      if (
        baselineRead.bytesRead !== targetRead.bytesRead
        || !baselineBuffer.subarray(0, baselineRead.bytesRead).equals(targetBuffer.subarray(0, targetRead.bytesRead))
      ) {
        equal = false
        break
      }
    }

    const [baselineAfter, targetAfter] = await Promise.all([
      baselineHandle.stat(),
      targetHandle.stat(),
    ])
    if (!hasRecordedFingerprint(baselineAfter, baseline) || !hasRecordedFingerprint(targetAfter, target))
      throw new Error('Comparison Entry changed while the snapshot was being built')
    return equal
  }
  finally {
    await Promise.all([baselineHandle.close(), targetHandle?.close()])
  }
}

async function refreshDiscoveredEntry(entry: DiscoveredEntry, absolutePath: string): Promise<DiscoveredEntry> {
  const stat = await lstat(absolutePath)
  if (stat.isFile()) {
    return {
      kind: 'file',
      size: stat.size,
      device: stat.dev,
      inode: stat.ino,
      modifiedAt: stat.mtimeMs,
      changedAt: stat.ctimeMs,
    }
  }
  if (stat.isSymbolicLink()) {
    return {
      kind: 'symlink',
      size: stat.size,
      linkTarget: await readlink(absolutePath),
      device: stat.dev,
      inode: stat.ino,
      modifiedAt: stat.mtimeMs,
      changedAt: stat.ctimeMs,
    }
  }
  throw new Error('Comparison Entry changed to an unsupported filesystem kind')
}

function hasSameDiscoveredIdentity(left: DiscoveredEntry, right: DiscoveredEntry): boolean {
  return left.kind === right.kind
    && left.size === right.size
    && left.device === right.device
    && left.inode === right.inode
    && left.modifiedAt === right.modifiedAt
    && left.changedAt === right.changedAt
    && left.linkTarget === right.linkTarget
}

async function assertDiscoveredEntryStable(entry: DiscoveredEntry, absolutePath: string): Promise<void> {
  let current: DiscoveredEntry
  try {
    current = await refreshDiscoveredEntry(entry, absolutePath)
  }
  catch {
    throw new Error('Comparison Entry changed before snapshot publication')
  }
  if (!hasSameDiscoveredIdentity(entry, current))
    throw new Error('Comparison Entry changed before snapshot publication')
}

async function compareWithRetry(
  baseline: DiscoveredEntry,
  target: DiscoveredEntry,
  baselinePath: string,
  targetPath: string,
  signal: AbortSignal,
  onBytesCompared: (bytes: number) => void,
): Promise<{ equal: boolean, baseline: DiscoveredEntry, target: DiscoveredEntry }> {
  let currentBaseline = baseline
  let currentTarget = target
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      return {
        equal: await hasEqualContent(
          currentBaseline,
          currentTarget,
          baselinePath,
          targetPath,
          signal,
          onBytesCompared,
        ),
        baseline: currentBaseline,
        target: currentTarget,
      }
    }
    catch (error) {
      if (signal.aborted)
        throw error
      if (attempt === 1)
        throw error
      const refreshed = await Promise.all([
        refreshDiscoveredEntry(currentBaseline, baselinePath),
        refreshDiscoveredEntry(currentTarget, targetPath),
      ])
      currentBaseline = refreshed[0]
      currentTarget = refreshed[1]
    }
  }
  throw new Error('Comparison Entry could not be compared')
}

function emptyCounts(): StatusCounts {
  return { added: 0, deleted: 0, modified: 0, unchanged: 0 }
}

function encodeCursor(index: number): string {
  return Buffer.from(`entry-index:${index}`).toString('base64url')
}

function decodeCursor(cursor: string | undefined): number {
  if (!cursor)
    return 0
  const decoded = Buffer.from(cursor, 'base64url').toString('utf8')
  const match = /^entry-index:(\d+)$/.exec(decoded)
  if (!match)
    throw new Error('Invalid entry cursor')
  return Number(match[1])
}

function addStatus(counts: StatusCounts, status: ComparisonStatus): void {
  counts[status]++
}

type DirectoryNode = Extract<TreeNode, { kind: 'directory' }>

interface MutableTreeChildren {
  directories: Map<string, DirectoryNode>
  entryIds: number[]
}

interface TreeChildren {
  directories: DirectoryNode[]
  entryIds: number[]
}

function buildTree(entries: ComparisonListEntry[]): Map<string, TreeChildren> {
  const childrenByDirectory = new Map<string, MutableTreeChildren>()
  const children = (directory: string): MutableTreeChildren => {
    let result = childrenByDirectory.get(directory)
    if (!result) {
      result = { directories: new Map(), entryIds: [] }
      childrenByDirectory.set(directory, result)
    }
    return result
  }

  for (const entry of entries) {
    const parts = entry.path.split('/')
    for (let index = 0; index < parts.length - 1; index++) {
      const parentPath = parts.slice(0, index).join('/')
      const directoryPath = parts.slice(0, index + 1).join('/')
      const parentChildren = children(parentPath)
      let node = parentChildren.directories.get(directoryPath)
      if (!node) {
        node = {
          kind: 'directory',
          name: parts[index]!,
          path: directoryPath,
          counts: emptyCounts(),
          issues: 0,
        }
        parentChildren.directories.set(directoryPath, node)
      }
      if (entry.status === 'issue')
        node.issues++
      else
        addStatus(node.counts, entry.status)
    }

    const parentPath = parts.slice(0, -1).join('/')
    children(parentPath).entryIds.push(entry.id)
  }

  return new Map([...childrenByDirectory].map(([directory, indexedChildren]) => [
    directory,
    {
      directories: [...indexedChildren.directories.values()].sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0),
      entryIds: indexedChildren.entryIds,
    },
  ]))
}

function absoluteComparisonPath(root: string, comparisonPath: string): string {
  return path.join(root, ...comparisonPath.split('/'))
}

function entryState(entry: DiscoveredEntry | undefined): ComparisonEntryState | undefined {
  if (!entry)
    return undefined
  return entry.kind === 'file'
    ? { kind: 'file', size: entry.size }
    : { kind: 'symlink', linkTarget: entry.linkTarget! }
}

function displayKind(entry: ComparisonEntry): ComparisonEntryKind {
  return entry.target?.kind ?? entry.baseline!.kind
}

function freezeListEntry(entry: ComparisonListEntry): ComparisonListEntry {
  if (entry.status === 'issue')
    return Object.freeze(entry)
  if (entry.baseline)
    Object.freeze(entry.baseline)
  if (entry.target)
    Object.freeze(entry.target)
  return Object.freeze(entry)
}

async function assertFixedDirectory(
  label: 'Baseline Directory' | 'Target Directory',
  directory: string,
  identity: DirectoryIdentity,
): Promise<void> {
  let stat: Awaited<ReturnType<typeof lstat>>
  try {
    stat = await lstat(directory)
  }
  catch {
    throw new Error(`${label} changed after the Comparison Workspace was created`)
  }
  if (!stat.isDirectory() || stat.dev !== identity.device || stat.ino !== identity.inode)
    throw new Error(`${label} changed after the Comparison Workspace was created`)
}

class Workspace implements ComparisonWorkspace {
  private publishedSnapshot?: ComparisonSnapshot
  private workspaceState: WorkspaceState = { phase: 'idle' }
  private readonly listeners = new Set<(state: WorkspaceState) => void>()
  private activeRefresh?: AbortController
  private activeTextDifferences = 0
  private lastProgressPublication = 0

  constructor(
    private readonly baselineDirectory: string,
    private readonly targetDirectory: string,
    private readonly rootIdentities: { baseline: DirectoryIdentity, target: DirectoryIdentity },
    private readonly useGitignore: boolean,
    private readonly exclusions: Bun.Glob[],
  ) {}

  state(): WorkspaceState {
    return {
      ...this.workspaceState,
      ...(this.workspaceState.progress ? { progress: { ...this.workspaceState.progress } } : {}),
    }
  }

  refresh(): RefreshRun {
    if (this.activeRefresh) {
      return {
        result: Promise.reject(new Error('A refresh is already active')),
        cancel() {},
      }
    }
    const controller = new AbortController()
    this.activeRefresh = controller
    const result = this.buildSnapshot(controller.signal).finally(() => {
      if (this.activeRefresh === controller)
        this.activeRefresh = undefined
    })
    return {
      result,
      cancel: () => controller.abort(),
    }
  }

  snapshot(id?: string): ComparisonSnapshot | undefined {
    if (id && id !== this.publishedSnapshot?.summary.id)
      return undefined
    return this.publishedSnapshot
  }

  observe(listener: (state: WorkspaceState) => void): () => void {
    this.listeners.add(listener)
    listener(this.state())
    return () => this.listeners.delete(listener)
  }

  private publishState(state: WorkspaceState): void {
    this.workspaceState = state
    for (const listener of this.listeners)
      listener(this.state())
  }

  private publishProgress(phase: 'discovering' | 'comparing', progress: WorkspaceProgress, force = false): void {
    const now = performance.now()
    if (!force && now - this.lastProgressPublication < 250)
      return
    this.lastProgressPublication = now
    this.publishState({ phase, progress: { ...progress } })
  }

  private async assertFixedRoots(): Promise<void> {
    await Promise.all([
      assertFixedDirectory('Baseline Directory', this.baselineDirectory, this.rootIdentities.baseline),
      assertFixedDirectory('Target Directory', this.targetDirectory, this.rootIdentities.target),
    ])
  }

  private async buildSnapshot(signal: AbortSignal): Promise<ComparisonSnapshot> {
    const progress: WorkspaceProgress = {
      discoveredEntries: 0,
      comparedEntries: 0,
      comparedBytes: 0,
      issues: 0,
    }
    this.publishProgress('discovering', progress, true)
    try {
      signal.throwIfAborted()
      await this.assertFixedRoots()
      const ignoreDiscovery: IgnoreDiscovery = this.useGitignore
        ? await collectTargetIgnoreMatchers(this.targetDirectory, this.exclusions, signal)
        : {
            matchers: new Map<string, IgnoreMatcher>(),
            issues: new Map<string, string>(),
            blockedDirectories: new Set<string>(),
          }
      const issueMessages = new Map(ignoreDiscovery.issues)
      progress.discoveredEntries += issueMessages.size
      progress.issues = issueMessages.size
      this.publishProgress('discovering', progress)

      const onDiscovered = (issue: boolean): void => {
        progress.discoveredEntries++
        if (issue)
          progress.issues++
        this.publishProgress('discovering', progress)
      }
      const [baselineDiscovery, targetDiscovery] = await Promise.all([
        discover(
          this.baselineDirectory,
          ignoreDiscovery.matchers,
          this.exclusions,
          ignoreDiscovery.blockedDirectories,
          'Baseline Directory',
          signal,
          onDiscovered,
        ),
        discover(
          this.targetDirectory,
          ignoreDiscovery.matchers,
          this.exclusions,
          ignoreDiscovery.blockedDirectories,
          'Target Directory',
          signal,
          onDiscovered,
        ),
      ])
      await this.assertFixedRoots()
      signal.throwIfAborted()
      mergeIssues(issueMessages, baselineDiscovery.issues)
      mergeIssues(issueMessages, targetDiscovery.issues)
      progress.issues = issueMessages.size

      const baselineEntries = baselineDiscovery.entries
      const targetEntries = targetDiscovery.entries
      const paths = [...new Set([
        ...baselineEntries.keys(),
        ...targetEntries.keys(),
        ...issueMessages.keys(),
      ])].sort()
      progress.totalEntries = paths.length
      progress.totalBytes = paths.reduce((total, comparisonPath) => {
        if (issueMessages.has(comparisonPath))
          return total
        const baseline = baselineEntries.get(comparisonPath)
        const target = targetEntries.get(comparisonPath)
        return baseline?.kind === 'file' && target?.kind === 'file' && baseline.size === target.size
          ? total + baseline.size
          : total
      }, 0)
      this.publishProgress('comparing', progress, true)

      const comparedEntries: Array<ComparisonListEntry | undefined> = Array.from({ length: paths.length })
      const baselineSources: Array<DiscoveredEntry | undefined> = Array.from({ length: paths.length })
      const targetSources: Array<DiscoveredEntry | undefined> = Array.from({ length: paths.length })
      let nextPathIndex = 0

      const compareNext = async (): Promise<void> => {
        while (nextPathIndex < paths.length) {
          signal.throwIfAborted()
          const index = nextPathIndex++
          const comparisonPath = paths[index]!
          let baseline = baselineEntries.get(comparisonPath)
          let target = targetEntries.get(comparisonPath)
          const discoveredIssue = issueMessages.get(comparisonPath)
          if (discoveredIssue) {
            comparedEntries[index] = {
              id: index + 1,
              path: comparisonPath,
              status: 'issue',
              kind: 'issue',
              message: discoveredIssue,
            }
            progress.comparedEntries++
            this.publishProgress('comparing', progress)
            continue
          }

          let status: ComparisonStatus
          if (!baseline) {
            status = 'added'
          }
          else if (!target) {
            status = 'deleted'
          }
          else {
            try {
              const comparison = await compareWithRetry(
                baseline,
                target,
                absoluteComparisonPath(this.baselineDirectory, comparisonPath),
                absoluteComparisonPath(this.targetDirectory, comparisonPath),
                signal,
                (bytes) => {
                  progress.comparedBytes += bytes
                  this.publishProgress('comparing', progress)
                },
              )
              baseline = comparison.baseline
              target = comparison.target
              baselineEntries.set(comparisonPath, baseline)
              targetEntries.set(comparisonPath, target)
              status = comparison.equal ? 'unchanged' : 'modified'
            }
            catch (error) {
              if (signal.aborted)
                throw error
              const message = error instanceof Error && error.message.startsWith('Comparison Entry')
                ? error.message
                : `Comparison could not be completed (${errorCode(error)})`
              comparedEntries[index] = {
                id: index + 1,
                path: comparisonPath,
                status: 'issue',
                kind: 'issue',
                message,
              }
              progress.comparedEntries++
              progress.issues++
              this.publishProgress('comparing', progress)
              continue
            }
          }

          baselineSources[index] = baseline
          targetSources[index] = target
          comparedEntries[index] = {
            id: index + 1,
            path: comparisonPath,
            status,
            baseline: entryState(baseline),
            target: entryState(target),
          }
          progress.comparedEntries++
          this.publishProgress('comparing', progress)
        }
      }
      await Promise.all(Array.from(
        { length: Math.min(8, Math.max(paths.length, 1)) },
        () => compareNext(),
      ))
      await this.assertFixedRoots()
      signal.throwIfAborted()
      await mapConcurrent(
        paths.map((comparisonPath, index) => ({ comparisonPath, index })),
        MAX_DIRECTORY_CONCURRENCY,
        async ({ comparisonPath, index }) => {
          signal.throwIfAborted()
          await Promise.all([
            baselineSources[index]
              ? assertDiscoveredEntryStable(
                  baselineSources[index],
                  absoluteComparisonPath(this.baselineDirectory, comparisonPath),
                )
              : undefined,
            targetSources[index]
              ? assertDiscoveredEntryStable(
                  targetSources[index],
                  absoluteComparisonPath(this.targetDirectory, comparisonPath),
                )
              : undefined,
          ])
        },
      )
      await this.assertFixedRoots()
      signal.throwIfAborted()

      const entries = comparedEntries
        .filter((entry): entry is ComparisonListEntry => entry !== undefined)
        .map(freezeListEntry)
      const counts = emptyCounts()
      let issueCount = 0
      for (const entry of entries) {
        if (entry.status === 'issue')
          issueCount++
        else
          counts[entry.status]++
      }

      progress.issues = issueCount
      progress.totalBytes = progress.comparedBytes
      this.publishState({ phase: 'publishing', progress: { ...progress } })
      const id = crypto.randomUUID()
      const tree = buildTree(entries)
      const baselineDirectory = this.baselineDirectory
      const targetDirectory = this.targetDirectory
      const entryById = (entryId: number): ComparisonListEntry | undefined => {
        const entry = entries[entryId - 1]
        return entry?.id === entryId ? entry : undefined
      }
      const entryTreeNode = (entry: ComparisonListEntry): Extract<TreeNode, { kind: 'file' | 'symlink' | 'issue' }> => ({
        kind: entry.status === 'issue' ? 'issue' : displayKind(entry),
        name: entry.path.slice(entry.path.lastIndexOf('/') + 1),
        path: entry.path,
        id: entry.id,
        status: entry.status,
        ...(entry.status === 'issue' ? { message: entry.message } : {}),
      })
      const directorySearchNodes = [...tree.values()].flatMap(indexedChildren => indexedChildren.directories)
      const summary = Object.freeze({
        id,
        baselineDirectory: this.baselineDirectory,
        targetDirectory: this.targetDirectory,
        createdAt: new Date().toISOString(),
        counts: Object.freeze({ ...counts }),
        issues: issueCount,
      })
      const snapshot: ComparisonSnapshot = {
        summary,
        list(query) {
          const pathSearch = query.path?.trim().toLowerCase()
          const matches = (entry: ComparisonListEntry): boolean => {
            const statusMatches = query.statuses?.length
              ? query.statuses.includes(entry.status)
              : query.includeUnchanged || entry.status !== 'unchanged'
            return statusMatches
              && (!query.kinds?.length || (
                entry.status === 'issue'
                  ? query.kinds.includes('issue')
                  : query.kinds.some(kind => kind !== 'issue' && (
                      entry.baseline?.kind === kind || entry.target?.kind === kind
                    ))
              ))
              && (!pathSearch || entry.path.toLowerCase().includes(pathSearch))
          }
          const limit = Math.min(Math.max(query.limit ?? 100, 1), 500)
          let startIndex = decodeCursor(query.cursor)
          if (query.anchor !== undefined) {
            const anchor = entryById(query.anchor)
            if (!anchor || !matches(anchor))
              throw new Error('Entry anchor does not match the current filters')
            startIndex = anchor.id - 1
          }
          const pageEntries: ComparisonListEntry[] = []
          let nextIndex: number | undefined
          for (let index = startIndex; index < entries.length; index++) {
            const entry = entries[index]!
            if (!matches(entry))
              continue
            if (pageEntries.length === limit) {
              nextIndex = index
              break
            }
            pageEntries.push(entry)
          }
          return {
            entries: pageEntries,
            ...(nextIndex !== undefined ? { nextCursor: encodeCursor(nextIndex) } : {}),
          }
        },
        tree(query) {
          const indexedChildren = tree.get(query.path)
          return {
            children: [
              ...(indexedChildren?.directories ?? []).map(node => ({ ...node, counts: { ...node.counts } })),
              ...(indexedChildren?.entryIds ?? []).flatMap((entryId) => {
                const entry = entryById(entryId)
                return entry
                  ? [entryTreeNode(entry)]
                  : []
              }),
            ],
          }
        },
        search(query, statuses, limit = 200) {
          const pathSearch = query.trim().toLowerCase()
          const boundedLimit = Math.min(Math.max(limit, 1), 200)
          const allowedStatuses = statuses ? new Set(statuses) : undefined
          const matchesStatus = (status: ComparisonListEntry['status']): boolean => !allowedStatuses || allowedStatuses.has(status)
          const directoryMatchesStatus = (node: DirectoryNode): boolean => !allowedStatuses || [...allowedStatuses].some(status => (
            status === 'issue' ? node.issues > 0 : node.counts[status] > 0
          ))
          const results: TreeNode[] = [
            ...directorySearchNodes
              .filter(node => directoryMatchesStatus(node) && node.path.toLowerCase().includes(pathSearch))
              .map(node => ({ ...node, counts: { ...node.counts } })),
            ...entries
              .filter(entry => matchesStatus(entry.status) && entry.path.toLowerCase().includes(pathSearch))
              .map(entryTreeNode),
          ].sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : left.kind === 'directory' ? -1 : 1)
          return {
            results: results.slice(0, boundedLimit),
            truncated: results.length > boundedLimit,
          }
        },
        async detail(entryId) {
          const entry = entryById(entryId)
          if (!entry)
            throw new Error('Comparison Entry not found')
          if (entry.status === 'issue')
            return { ...entry, presentation: 'issue' }
          const sourceIndex = entryId - 1
          const baseline = baselineSources[sourceIndex]
          const target = targetSources[sourceIndex]
          return {
            ...entry,
            presentation: await classifyPresentation(
              entry,
              baseline,
              target,
              absoluteComparisonPath(baselineDirectory, entry.path),
              absoluteComparisonPath(targetDirectory, entry.path),
            ),
          }
        },
        async content(entryId: number, side: ComparisonSide, force = false) {
          const entry = entryById(entryId)
          if (!entry)
            throw new Error('Comparison Entry not found')
          if (entry.status === 'issue')
            throw new Error('Comparison Issue has no text content')
          const root = side === 'baseline' ? baselineDirectory : targetDirectory
          return loadTextContent(
            side === 'baseline' ? baselineSources[entryId - 1] : targetSources[entryId - 1],
            absoluteComparisonPath(root, entry.path),
            force,
          )
        },
        textDiff: async (entryId, options): Promise<TextDiffResult> => {
          const entry = entryById(entryId)
          if (!entry || entry.status === 'issue' || entry.status === 'unchanged')
            throw new Error('Comparison Entry not found')
          const contextLines = options?.contextLines ?? 3
          if (!Number.isInteger(contextLines) || contextLines < 0 || contextLines > 20)
            throw new Error('contextLines must be an integer between 0 and 20')
          if (entry.baseline && entry.target && entry.baseline.kind !== entry.target.kind) {
            return {
              status: 'unavailable',
              path: entry.path,
              comparisonStatus: entry.status,
              reason: 'mixed_entry_kinds',
            }
          }
          if (this.activeTextDifferences >= 2) {
            return {
              status: 'unavailable',
              path: entry.path,
              comparisonStatus: entry.status,
              reason: 'server_busy',
            }
          }
          this.activeTextDifferences++
          try {
            const sourceIndex = entryId - 1
            const [baselineContent, targetContent] = await Promise.all([
              loadTextContent(
                baselineSources[sourceIndex],
                absoluteComparisonPath(baselineDirectory, entry.path),
                false,
              ),
              loadTextContent(
                targetSources[sourceIndex],
                absoluteComparisonPath(targetDirectory, entry.path),
                false,
              ),
            ])
            if (baselineContent.status === 'binary' || targetContent.status === 'binary') {
              return {
                status: 'unavailable',
                path: entry.path,
                comparisonStatus: entry.status,
                reason: 'non_text',
              }
            }
            if (
              baselineContent.status === 'guarded' || baselineContent.status === 'blocked'
              || targetContent.status === 'guarded' || targetContent.status === 'blocked'
            ) {
              return {
                status: 'unavailable',
                path: entry.path,
                comparisonStatus: entry.status,
                reason: 'source_too_large',
                ...(baselineContent.status === 'guarded' || baselineContent.status === 'blocked'
                  ? {
                      baselineSize: baselineContent.size,
                      ...('lineCount' in baselineContent && baselineContent.lineCount !== undefined
                        ? { baselineLineCount: baselineContent.lineCount }
                        : {}),
                    }
                  : {}),
                ...(targetContent.status === 'guarded' || targetContent.status === 'blocked'
                  ? {
                      targetSize: targetContent.size,
                      ...('lineCount' in targetContent && targetContent.lineCount !== undefined
                        ? { targetLineCount: targetContent.lineCount }
                        : {}),
                    }
                  : {}),
              }
            }
            if (baselineContent.status === 'stale' || targetContent.status === 'stale') {
              return {
                status: 'unavailable',
                path: entry.path,
                comparisonStatus: entry.status,
                reason: 'stale',
              }
            }
            if (!['ready', 'missing'].includes(baselineContent.status) || !['ready', 'missing'].includes(targetContent.status))
              throw new Error('Comparison Entry does not have readable text on both sides')
            const baselineText = baselineContent.status === 'ready' ? baselineContent.text : ''
            const targetText = targetContent.status === 'ready' ? targetContent.text : ''
            if (
              entry.status === 'modified'
              && baselineContent.status === 'ready'
              && targetContent.status === 'ready'
              && baselineText === targetText
            ) {
              return {
                status: 'no_textual_changes',
                path: entry.path,
                comparisonStatus: 'modified',
                reason: 'encoding_or_bom_only',
                baselineEncoding: baselineContent.encoding,
                targetEncoding: targetContent.encoding,
              }
            }

            const structured = await new Promise<NonNullable<ReturnType<typeof structuredPatch>> | undefined>((resolve) => {
              structuredPatch(
                baselineContent.status === 'missing' ? '/dev/null' : 'baseline',
                targetContent.status === 'missing' ? '/dev/null' : 'target',
                baselineText,
                targetText,
                undefined,
                undefined,
                { context: contextLines, timeout: 5_000, callback: resolve },
              )
            })
            if (!structured) {
              return {
                status: 'unavailable',
                path: entry.path,
                comparisonStatus: entry.status,
                reason: 'complexity_limit',
              }
            }
            const addedLines = structured.hunks.reduce((count, hunk) => count + hunk.lines.filter(line => line.startsWith('+')).length, 0)
            const deletedLines = structured.hunks.reduce((count, hunk) => count + hunk.lines.filter(line => line.startsWith('-')).length, 0)
            const patch = formatPatch(structured, FILE_HEADERS_ONLY)
            const outputBytes = Buffer.byteLength(patch)
            if (outputBytes > 256 * 1024) {
              return {
                status: 'unavailable',
                path: entry.path,
                comparisonStatus: entry.status,
                reason: 'output_too_large',
                addedLines,
                deletedLines,
                outputBytes,
              }
            }
            return {
              status: 'ready',
              path: entry.path,
              comparisonStatus: entry.status,
              contextLines,
              ...(baselineContent.status === 'ready' ? { baselineEncoding: baselineContent.encoding } : {}),
              ...(targetContent.status === 'ready' ? { targetEncoding: targetContent.encoding } : {}),
              addedLines,
              deletedLines,
              patch,
            }
          }
          finally {
            this.activeTextDifferences--
          }
        },
        async blob(entryId: number, side: ComparisonSide): Promise<BlobContent> {
          const entry = entryById(entryId)
          if (!entry)
            throw new Error('Comparison Entry not found')
          if (entry.status === 'issue')
            return { status: 'unavailable' }
          const source = side === 'baseline' ? baselineSources[entryId - 1] : targetSources[entryId - 1]
          if (!source)
            return { status: 'missing' }
          if (source.kind !== 'file')
            return { status: 'unavailable' }
          const root = side === 'baseline' ? baselineDirectory : targetDirectory
          const bytes = await readStableBytes(source, absoluteComparisonPath(root, entry.path))
          if (bytes === 'stale')
            return { status: 'stale' }
          return {
            status: 'ready',
            bytes,
            mimeType: IMAGE_MIME_TYPES.get(path.extname(entry.path).toLowerCase()) ?? 'application/octet-stream',
            filename: path.basename(entry.path),
          }
        },
      }
      Object.freeze(snapshot)

      this.publishedSnapshot = snapshot
      this.publishState({ phase: 'ready', snapshotId: id })
      return snapshot
    }
    catch (error) {
      if (signal.aborted) {
        this.publishState(this.publishedSnapshot
          ? { phase: 'canceled', snapshotId: this.publishedSnapshot.summary.id }
          : { phase: 'canceled' })
        throw error
      }
      this.publishState({
        phase: 'error',
        error: error instanceof Error ? error.message : String(error),
        ...(this.publishedSnapshot ? { snapshotId: this.publishedSnapshot.summary.id } : {}),
      })
      throw error
    }
  }
}

export async function createComparisonWorkspace(options: ComparisonWorkspaceOptions): Promise<ComparisonWorkspace> {
  const [baselineDirectory, targetDirectory] = await Promise.all([
    realpath(options.baselineDirectory),
    realpath(options.targetDirectory),
  ])
  const [baselineStat, targetStat] = await Promise.all([
    lstat(baselineDirectory),
    lstat(targetDirectory),
  ])
  if (!baselineStat.isDirectory())
    throw new Error('Baseline Directory must be a directory')
  if (!targetStat.isDirectory())
    throw new Error('Target Directory must be a directory')
  if (baselineDirectory === targetDirectory)
    throw new Error('Baseline Directory and Target Directory must be different')
  return new Workspace(
    baselineDirectory,
    targetDirectory,
    {
      baseline: { device: baselineStat.dev, inode: baselineStat.ino },
      target: { device: targetStat.dev, inode: targetStat.ino },
    },
    options.gitignore !== false,
    (options.exclusions ?? []).map(pattern => new Bun.Glob(pattern)),
  )
}
