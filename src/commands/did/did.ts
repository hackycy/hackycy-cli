import type { DidOptions } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import { cancel, isCancel, log, select, spinner, text } from '@clack/prompts'
import dayjs from 'dayjs'
import { printTitle } from '../../shared/utils'

interface CommitLogRow {
  repository: string
  author: string
  date: string
  message: string
}

export async function findMyDid(opt: DidOptions & { root: string }): Promise<void> {
  printTitle()

  if (!(await testGit())) {
    cancel('Git is not installed or not available in the system PATH.')
    return
  }

  const root = path.resolve(opt.root)

  // 判断当前目录是否存在及判断是否为目录
  if (!(await fs.exists(root)) || !(await fs.stat(root)).isDirectory()) {
    cancel('The specified path is not a valid directory.')
    return
  }

  // .git 目录集合
  const collectedGitDirs: string[] = []

  // 查找当前是否存在 .git 目录
  if (await isGitRepository(root)) {
    collectedGitDirs.push(root)
  }
  else {
    const spin = spinner()
    spin.start('Searching for Git repositories...')
    // 递归查找子目录中的 .git 目录，直到达到指定深度
    await findGitDirsRecursively(root, opt.depth, collectedGitDirs)
    spin.stop('Git repository search completed.')
  }

  if (collectedGitDirs.length === 0) {
    cancel('No Git repositories found within the specified depth.')
    return
  }

  log.info(`Found ${collectedGitDirs.length} Git repositories`)

  // 选择查找几天前的提交
  const selected = await select<number | 'custom'>({
    message: 'Select the age of commits to find:',
    options: [
      { value: 1, label: 'Today' },
      { value: 2, label: 'Yesterday' },
      { value: 3, label: '3 days ago' },
      { value: 'custom', label: 'Custom' },
    ],
  })

  if (isCancel(selected)) {
    cancel('Operation cancelled.')
    return
  }

  let days: number

  if (selected === 'custom') {
    const input = await text({
      message: 'Enter the number of days:',
      placeholder: 'e.g., 5',
      validate(value) {
        const num = Number(value)
        if (Number.isNaN(num) || num <= 0)
          return 'Please enter a valid positive number.'
        return undefined
      },
    })

    if (isCancel(input)) {
      cancel('Operation cancelled.')
      return
    }

    days = Number(input)
  }
  else {
    days = selected
  }

  // 解析日期
  const findDate = dayjs().subtract(days, 'day').format('YYYY-MM-DD 00:00:00')

  const logRows: CommitLogRow[] = []
  const repositoriesWithCommits = new Set<string>()

  const spin = spinner()
  spin.start('Collecting commit logs...')

  for (const gitDir of collectedGitDirs) {
    try {
      const repositoryName = path.relative(root, gitDir) || '.'
      const proc = Bun.spawn({
        cmd: ['git', '-C', gitDir, 'log', `--since=${findDate}`, '--date=format:%Y-%m-%d %H:%M:%S', '--pretty=format:%an%x1f%ad%x1f%s'],
        stdout: 'pipe',
        stderr: 'pipe',
      })

      const { stdout, stderr } = proc

      const outText = await new Response(stdout).text()
      const errText = await new Response(stderr).text()

      await proc.exited

      if (proc.exitCode !== 0) {
        log.warn(`Git command failed in ${gitDir}: ${errText.trim()}`)
        continue
      }

      const logs = outText.trim().split('\n').filter(line => line.length > 0)
      let isFirstRowInRepository = true

      for (const line of logs) {
        const [author, date, ...messageParts] = line.split('\u001F')
        const message = messageParts.join('\u001F')

        if (!author || !date || !message) {
          continue
        }

        repositoriesWithCommits.add(repositoryName)

        logRows.push({
          repository: isFirstRowInRepository ? repositoryName : '',
          author,
          date,
          message,
        })

        isFirstRowInRepository = false
      }
    }
    catch (error) {
      log.warn(`Error processing ${gitDir}: ${(error as Error).message}`)
    }
  }

  spin.stop('Commit log collection completed.')

  if (logRows.length === 0) {
    cancel('No commits found in the specified date range.')
    return
  }

  const repositoryCount = repositoriesWithCommits.size

  log.success(`Found ${logRows.length} commits in ${repositoryCount} repositories:\n`)
  console.table(logRows)
}

/**
 * 递归查找 Git 目录
 */
async function findGitDirsRecursively(dir: string, depth: number, collected: string[]): Promise<void> {
  if (depth < 0) {
    return
  }

  const entries = await fs.readdir(dir, { withFileTypes: true })

  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name)

    if (entry.isDirectory()) {
      if (entry.name === '.git') {
        collected.push(dir)
      }
      else {
        await findGitDirsRecursively(fullPath, depth - 1, collected)
      }
    }
  }
}

/**
 * 是否为 Git 仓库
 */
async function isGitRepository(dir: string): Promise<boolean> {
  const gitPath = path.join(dir, '.git')
  return fs
    .stat(gitPath)
    .then(stats => stats.isDirectory())
    .catch(() => false)
}

async function testGit(): Promise<boolean> {
  // 检测git命令是否可用
  try {
    const proc = Bun.spawn(['git', '--version'])
    await proc.exited
    return proc.exitCode === 0
  }
  catch {
    return false
  }
}
