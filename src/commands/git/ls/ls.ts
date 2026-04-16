import type { CommitRecord, GitLsOptions } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { cancel, intro, isCancel, log, multiselect, select, spinner } from '@clack/prompts'
import ansis from 'ansis'
import dayjs from 'dayjs'
import { render } from 'ink'
import React from 'react'
import { printTitle } from '../../../shared/utils'
import { CommitTree } from './components/CommitTree'

const EXCLUDED_DIRS = new Set([
  'node_modules',
  'vendor',
  'dist',
  '.cache',
  'Library',
  '.Trash',
  'bower_components',
  '__pycache__',
  '.venv',
  'venv',
])

const CONCURRENCY = 5
const SCAN_PROGRESS_BATCH_SIZE = 100

function yieldToEventLoop(): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, 0))
}

export async function scanRepos(
  directory: string,
  onFound: (repoPath: string) => void,
): Promise<string[]> {
  const repos: string[] = []
  const stack = [directory]
  let scanned = 0

  while (stack.length > 0) {
    const currentDir = stack.pop()!
    scanned++

    let entries
    try {
      entries = await fs.readdir(currentDir, { withFileTypes: true })
    }
    catch {
      continue
    }

    let hasGitDir = false

    for (const entry of entries) {
      if (!entry.isDirectory())
        continue

      if (entry.name === '.git') {
        hasGitDir = true
        continue
      }

      // Skip heavy directories before descending into them.
      if (EXCLUDED_DIRS.has(entry.name))
        continue

      stack.push(path.join(currentDir, entry.name))
    }

    if (hasGitDir) {
      repos.push(currentDir)
      onFound(currentDir)
    }

    // Yield periodically so long scans keep the terminal responsive.
    if (scanned % SCAN_PROGRESS_BATCH_SIZE === 0)
      await yieldToEventLoop()
  }

  return repos
}

export async function fetchCommits(
  repos: string[],
  days: number,
  root: string,
  onProgress: (repo: string, done: number) => void,
): Promise<CommitRecord[]> {
  const sinceDate = dayjs().startOf('day').subtract(days - 1, 'day').format('YYYY-MM-DD HH:mm:ss')
  const allCommits: CommitRecord[] = []
  let running = 0
  let done = 0
  let idx = 0
  const queue: Array<() => void> = []

  function tryNext() {
    while (running < CONCURRENCY && idx < repos.length) {
      const repo = repos[idx++]!
      running++
      processRepo(repo).finally(() => {
        running--
        done++
        onProgress(repo, done)
        if (queue.length > 0)
          queue.shift()!()
      })
    }
  }

  async function processRepo(repoDir: string) {
    try {
      const proc = Bun.spawn({
        cmd: [
          'git',
          '-C',
          repoDir,
          'log',
          `--since=${sinceDate}`,
          '--date=format:%Y-%m-%d %H:%M:%S',
          '--pretty=format:%an\x1F%ad\x1F%s',
        ],
        stdout: 'pipe',
        stderr: 'pipe',
      })

      const outText = await new Response(proc.stdout).text()
      await proc.exited

      if (proc.exitCode !== 0)
        return

      const repoName = path.relative(root, repoDir) || '.'
      const lines = outText.trim().split('\n').filter(l => l.length > 0)

      for (const line of lines) {
        const parts = line.split('\x1F')
        if (parts.length < 3)
          continue
        const [author, date, ...msgParts] = parts
        allCommits.push({
          repo: repoName,
          author: author!,
          date: date!,
          message: msgParts.join('\x1F'),
        })
      }
    }
    catch {
      // skip repos that fail
    }
  }

  return new Promise((resolve) => {
    if (repos.length === 0) {
      resolve([])
      return
    }

    const check = setInterval(() => {
      tryNext()
      if (done >= repos.length) {
        clearInterval(check)
        resolve(allCommits)
      }
    }, 10)

    tryNext()
  })
}

export function getAuthors(commits: CommitRecord[]): string[] {
  return [...new Set(commits.map(c => c.author))].sort()
}

export async function testGit(): Promise<boolean> {
  try {
    const proc = Bun.spawn(['git', '--version'])
    await proc.exited
    return proc.exitCode === 0
  }
  catch {
    return false
  }
}

export async function runGitLs(directory: string, options: GitLsOptions): Promise<void> {
  const root = path.resolve(directory)

  let rootStat
  try {
    rootStat = await fs.stat(root)
  }
  catch {
    cancel(`Directory not found: ${ansis.dim(root)}`)
    process.exit(1)
  }

  if (!rootStat.isDirectory()) {
    cancel(`Path is not a directory: ${ansis.dim(root)}`)
    process.exit(1)
  }

  if (!(await testGit())) {
    cancel('Git is not installed or not available in the system PATH.')
    process.exit(1)
  }

  printTitle()
  intro(ansis.bold('Git Commit Tree'))
  log.info(`Workspace: ${ansis.cyan(root)}`)

  const scanSpin = spinner()
  let foundCount = 0
  scanSpin.start('Scanning repositories...')

  const repos = await scanRepos(root, (repoPath) => {
    foundCount += 1
    const relativePath = path.relative(root, repoPath) || '.'
    scanSpin.message(`Scanning repositories... [${foundCount}] ${ansis.dim(relativePath)}`)
  })

  if (repos.length === 0) {
    scanSpin.stop('No Git repositories found.')
    return
  }

  scanSpin.stop(`Found ${repos.length} repositor${repos.length === 1 ? 'y' : 'ies'}`)

  const days = options.days ?? await promptForDays()

  const fetchSpin = spinner()
  fetchSpin.start(`Fetching commits... [0/${repos.length}]`)

  const commits = await fetchCommits(repos, days, root, (repo, done) => {
    const relativePath = path.relative(root, repo) || '.'
    fetchSpin.message(`Fetching commits... [${done}/${repos.length}] ${ansis.dim(relativePath)}`)
  })

  if (commits.length === 0) {
    fetchSpin.stop('No commits found in the specified date range.')
    return
  }

  fetchSpin.stop(`Found ${commits.length} commit${commits.length === 1 ? '' : 's'}`)

  const filteredCommits = await promptForAuthors(commits)

  console.log()
  await renderCommitTree(filteredCommits)
}

async function promptForDays(): Promise<number> {
  const selectedDays = await select({
    message: 'Select date range:',
    options: [
      { value: 1, label: 'Today' },
      { value: 2, label: 'Yesterday' },
      { value: 3, label: 'Last 3 days' },
      { value: 7, label: 'Last 7 days' },
      { value: 30, label: 'Last 30 days' },
    ],
  })

  if (isCancel(selectedDays)) {
    cancel('Operation cancelled.')
    process.exit(0)
  }

  return selectedDays
}

async function promptForAuthors(commits: CommitRecord[]): Promise<CommitRecord[]> {
  const authors = getAuthors(commits)
  if (authors.length <= 1) {
    return commits
  }

  const selectedAuthors = await multiselect({
    message: 'Filter by authors:',
    options: authors.map(author => ({
      value: author,
      label: author,
    })),
    initialValues: authors,
    required: true,
  })

  if (isCancel(selectedAuthors)) {
    cancel('Operation cancelled.')
    process.exit(0)
  }

  return commits.filter(commit => selectedAuthors.includes(commit.author))
}

async function renderCommitTree(commits: CommitRecord[]): Promise<void> {
  let unmount: (() => void) | undefined

  const inst = render(React.createElement(CommitTree, {
    commits,
    onDone: () => unmount?.(),
  }))

  unmount = inst.unmount
  await inst.waitUntilExit()
}
