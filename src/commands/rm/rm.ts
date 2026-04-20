import type { RmOptions } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { cancel, confirm, intro, isCancel, outro, select, spinner } from '@clack/prompts'
import ansis from 'ansis'
import { printTitle } from '../../shared/utils'
import { scanForCandidates } from './scanner'

async function deletePaths(targets: string[]): Promise<void> {
  const spin = spinner()
  spin.start(`Deleting ${targets.length} item${targets.length !== 1 ? 's' : ''}...`)

  const results = await Promise.allSettled(
    targets.map(async (p) => {
      await fs.rm(p, { recursive: true, force: true })
      return p
    }),
  )

  const failures: string[] = []
  for (const result of results) {
    if (result.status === 'rejected')
      failures.push(String(result.reason))
  }

  const succeeded = targets.length - failures.length
  spin.stop(`Deleted ${succeeded} item${succeeded !== 1 ? 's' : ''}`)

  for (const f of failures)
    console.log(ansis.yellow(`  skipped: ${f}`))
}

export async function rm(paths: string[], options: RmOptions): Promise<void> {
  printTitle()
  intro(ansis.bold('Remove'))

  if (paths.length > 0) {
    // Explicit path mode: resolve and verify each path
    const absPaths = paths.map(p => path.resolve(p))
    const existing: string[] = []

    for (const p of absPaths) {
      try {
        await fs.access(p)
        existing.push(p)
      }
      catch {
        console.log(ansis.yellow(`  not found, skipping: ${p}`))
      }
    }

    if (existing.length === 0) {
      cancel('No valid paths to delete.')
      return
    }

    if (!options.force) {
      console.log()
      for (const p of existing)
        console.log(ansis.dim(`  ${p}`))
      console.log()

      const ok = await confirm({
        message: `Delete ${existing.length} item${existing.length !== 1 ? 's' : ''}?`,
        initialValue: false,
      })

      if (isCancel(ok) || !ok) {
        cancel('Cancelled.')
        return
      }
    }

    await deletePaths(existing)
    outro(ansis.green('Done!'))
    return
  }

  // Smart mode: scan current directory for cleanable targets
  const cwd = process.cwd()
  const depth = options.depth ?? 5

  const scanSpin = spinner()
  scanSpin.start('Scanning for cleanable targets...')
  const candidates = await scanForCandidates(cwd, depth)
  scanSpin.stop(
    candidates.length > 0
      ? `Found ${candidates.length} target${candidates.length !== 1 ? 's' : ''}`
      : 'No cleanable targets found.',
  )

  if (candidates.length === 0) {
    outro('Nothing to clean.')
    return
  }

  const selected = await select({
    message: 'Select item to delete',
    options: candidates.map(c => ({
      value: c.path,
      label: path.relative(cwd, c.path),
      hint: `[${c.rule.category}] ${c.rule.label}`,
    })),
  })

  if (isCancel(selected)) {
    cancel('Cancelled.')
    return
  }

  await deletePaths([selected as string])
  outro(ansis.green('Done!'))
}
