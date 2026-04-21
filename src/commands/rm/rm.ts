import type { RmOptions } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { cancel, confirm, intro, isCancel, multiselect, outro, select, spinner } from '@clack/prompts'
import ansis from 'ansis'
import { printTitle } from '../../shared/utils'
import { CLEAN_ACTIONS } from './rules'

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

  // Smart mode: select a clean action, scan, then multiselect targets
  const cwd = process.cwd()
  const depth = options.depth ?? 5

  const actionChoice = await select({
    message: 'Select a clean action',
    options: CLEAN_ACTIONS.map(a => ({
      value: a.id,
      label: a.label,
    })),
  })

  if (isCancel(actionChoice)) {
    cancel('Cancelled.')
    return
  }

  const action = CLEAN_ACTIONS.find(a => a.id === actionChoice)!

  const scanSpin = spinner()
  scanSpin.start('Scanning...')
  const targets = await action.scan(cwd, depth)
  scanSpin.stop(
    targets.length > 0
      ? `Found ${targets.length} target${targets.length !== 1 ? 's' : ''}`
      : 'No targets found.',
  )

  if (targets.length === 0) {
    outro('Nothing to clean.')
    return
  }

  let toDelete: string[]

  if (options.force) {
    toDelete = targets
  }
  else {
    const selected = await multiselect({
      message: 'Select items to delete',
      options: targets.map(p => ({
        value: p,
        label: path.relative(cwd, p),
      })),
      initialValues: targets,
    })

    if (isCancel(selected)) {
      cancel('Cancelled.')
      return
    }

    toDelete = selected as string[]

    if (toDelete.length === 0) {
      cancel('Nothing selected.')
      return
    }
  }

  await deletePaths(toDelete)
  outro(ansis.green('Done!'))
}
