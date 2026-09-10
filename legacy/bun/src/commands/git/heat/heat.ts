import type { ChangeCounts, ChangeKind, GitHeatOptions, HeatReport, PathHeat } from './types'
import path from 'node:path'
import process from 'node:process'
import { log } from '@clack/prompts'
import { render } from 'ink'
import React from 'react'
import { printTitle } from '../../../shared/utils'
import { HeatReportView } from './components/HeatReportView'

const DEFAULT_LIMIT = 20

export async function runGitHeat(options: GitHeatOptions): Promise<void> {
  if (options.limit !== undefined && options.days !== undefined) {
    log.error('Please use either -n/--limit or -d/--days, not both.')
    process.exit(1)
  }

  const limit = options.limit ?? (options.days === undefined ? DEFAULT_LIMIT : undefined)
  validatePositiveInteger(limit, '-n/--limit')
  validatePositiveInteger(options.days, '-d/--days')
  const query = options.query?.trim()

  let repoRoot: string
  try {
    repoRoot = await getRepoRoot()
  }
  catch (err) {
    log.error((err as Error).message)
    process.exit(1)
  }

  let report: HeatReport
  try {
    report = buildHeatReport(repoRoot, await readGitLog(repoRoot, { limit, days: options.days }), {
      limit,
      days: options.days,
      target: options.type ?? 'files',
      sort: options.sort ?? 'count',
      relativeTime: options.relativeTime ?? false,
      query: query || undefined,
    })
  }
  catch (err) {
    log.error((err as Error).message)
    process.exit(1)
  }

  if (report.commitCount === 0 || report.files.length === 0) {
    log.info('No changed files found in the selected range.')
    return
  }

  printTitle()
  await renderHeatReport(report)
}

async function getRepoRoot(): Promise<string> {
  const proc = Bun.spawn(['git', 'rev-parse', '--show-toplevel'], {
    stdout: 'pipe',
    stderr: 'pipe',
  })

  const stdout = await new Response(proc.stdout).text()
  const stderr = await new Response(proc.stderr).text()
  await proc.exited

  if (proc.exitCode !== 0)
    throw new Error(stderr.trim() || 'Current directory is not inside a Git repository.')

  return stdout.trim()
}

async function readGitLog(repoRoot: string, range: { limit?: number, days?: number }): Promise<string> {
  const rangeArgs = range.days !== undefined
    ? [`--since=${range.days} days ago`]
    : ['-n', String(range.limit ?? DEFAULT_LIMIT)]

  const proc = Bun.spawn([
    'git',
    '-C',
    repoRoot,
    'log',
    ...rangeArgs,
    '--name-status',
    '--pretty=format:__HACKYCY_HEAT_COMMIT__%H%x1f%ct%x1f%ci',
  ], {
    stdout: 'pipe',
    stderr: 'pipe',
  })

  const stdout = await new Response(proc.stdout).text()
  const stderr = await new Response(proc.stderr).text()
  await proc.exited

  if (proc.exitCode !== 0)
    throw new Error(stderr.trim() || 'Failed to read git log.')

  return stdout
}

export function buildHeatReport(
  repoRoot: string,
  gitLog: string,
  range: { limit?: number, days?: number, target: HeatReport['target'], sort: HeatReport['sort'], relativeTime: boolean, query?: string },
): HeatReport {
  const files = new Map<string, PathHeat>()
  let commitCount = 0
  let currentCommitTime: CommitTime | undefined

  for (const rawLine of gitLog.split('\n')) {
    const line = rawLine.trim()
    if (!line)
      continue

    if (line.startsWith('__HACKYCY_HEAT_COMMIT__')) {
      commitCount += 1
      currentCommitTime = parseCommitTime(line)
      continue
    }

    const parsed = parseNameStatusLine(line)
    if (!parsed)
      continue

    incrementPath(files, parsed.path, parsed.kind, currentCommitTime)
  }

  const fileRows = sortFileRows([...files.values()], range.sort)
  const directoryRows = buildDirectoryRows(fileRows, range.sort)

  return {
    repoRoot,
    repoName: path.basename(repoRoot),
    rangeLabel: range.days !== undefined
      ? `last ${range.days} day${range.days === 1 ? '' : 's'}`
      : `last ${range.limit ?? DEFAULT_LIMIT} commits`,
    target: range.target,
    sort: range.sort,
    relativeTime: range.relativeTime,
    query: range.query,
    commitCount,
    files: fileRows,
    directories: directoryRows,
  }
}

interface CommitTime {
  label: string
  epoch: number
}

function parseCommitTime(line: string): CommitTime {
  const [, epoch, label = ''] = line.split('\x1F')
  return {
    label: label.slice(0, 19),
    epoch: Number(epoch) || 0,
  }
}

function parseNameStatusLine(line: string): { kind: ChangeKind, path: string } | undefined {
  const parts = line.split('\t').filter(Boolean)
  const rawStatus = parts[0]
  if (!rawStatus)
    return undefined

  const kind = rawStatus[0] as ChangeKind
  if (!isSupportedKind(kind))
    return undefined

  if ((kind === 'R' || kind === 'C') && parts[2])
    return { kind, path: parts[2] }

  const filePath = parts[1]
  if (!filePath)
    return undefined

  return { kind, path: filePath }
}

function isSupportedKind(kind: string): kind is ChangeKind {
  return kind === 'M' || kind === 'A' || kind === 'D' || kind === 'R' || kind === 'C'
}

function buildDirectoryRows(files: PathHeat[], sort: HeatReport['sort']): PathHeat[] {
  const directories = new Map<string, PathHeat>()

  for (const file of files) {
    const dir = path.dirname(file.path)
    const key = dir === '.' ? '.' : dir
    const row = getOrCreatePathHeat(directories, key)
    row.total += file.total
    row.modified += file.modified
    row.added += file.added
    row.deleted += file.deleted
    row.renamed += file.renamed
    row.copied += file.copied
    if (file.lastChangedAtEpoch > row.lastChangedAtEpoch) {
      row.lastChangedAt = file.lastChangedAt
      row.lastChangedAtEpoch = file.lastChangedAtEpoch
    }
  }

  return sortDirectoryRows([...directories.values()], sort)
}

function incrementPath(map: Map<string, PathHeat>, filePath: string, kind: ChangeKind, commitTime: CommitTime | undefined): void {
  const row = getOrCreatePathHeat(map, filePath)
  row.total += 1
  if (commitTime && commitTime.epoch >= row.lastChangedAtEpoch) {
    row.lastChangedAt = commitTime.label
    row.lastChangedAtEpoch = commitTime.epoch
  }

  switch (kind) {
    case 'M':
      row.modified += 1
      break
    case 'A':
      row.added += 1
      break
    case 'D':
      row.deleted += 1
      break
    case 'R':
      row.renamed += 1
      break
    case 'C':
      row.copied += 1
      break
  }
}

function getOrCreatePathHeat(map: Map<string, PathHeat>, filePath: string): PathHeat {
  let row = map.get(filePath)
  if (!row) {
    row = {
      path: filePath,
      lastChangedAt: '',
      lastChangedAtEpoch: 0,
      ...emptyCounts(),
    }
    map.set(filePath, row)
  }
  return row
}

function emptyCounts(): ChangeCounts {
  return {
    total: 0,
    modified: 0,
    added: 0,
    deleted: 0,
    renamed: 0,
    copied: 0,
  }
}

function sortFileRows(rows: PathHeat[], sort: HeatReport['sort']): PathHeat[] {
  return rows.sort((a, b) => {
    if (sort === 'path')
      return compareFilePath(a.path, b.path)
    if (b.total !== a.total)
      return b.total - a.total
    return a.path.localeCompare(b.path)
  })
}

function sortDirectoryRows(rows: PathHeat[], sort: HeatReport['sort']): PathHeat[] {
  return rows.sort((a, b) => {
    if (sort === 'path')
      return compareDirectoryPath(a.path, b.path)
    if (b.total !== a.total)
      return b.total - a.total
    return a.path.localeCompare(b.path)
  })
}

function compareFilePath(a: string, b: string): number {
  const aIsRootFile = !a.includes('/')
  const bIsRootFile = !b.includes('/')

  if (aIsRootFile !== bIsRootFile)
    return aIsRootFile ? 1 : -1

  return a.localeCompare(b)
}

function compareDirectoryPath(a: string, b: string): number {
  if (a === '.' && b !== '.')
    return 1
  if (b === '.' && a !== '.')
    return -1
  return a.localeCompare(b)
}

function validatePositiveInteger(value: number | undefined, label: string): void {
  if (value === undefined)
    return
  if (!Number.isInteger(value) || value <= 0) {
    log.error(`${label} must be a positive integer.`)
    process.exit(1)
  }
}

async function renderHeatReport(report: HeatReport): Promise<void> {
  let unmount: (() => void) | undefined

  const inst = render(React.createElement(HeatReportView, {
    report,
    onDone: () => unmount?.(),
  }))

  unmount = inst.unmount
  await inst.waitUntilExit()
}
