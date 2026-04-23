import type { PackageManager, RunOptions } from './types'
import path from 'node:path'
import process from 'node:process'
import { cancel, intro, isCancel, log, select } from '@clack/prompts'
import ansis from 'ansis'
import { printTitle } from '../../shared/utils'

const LOCKFILE_PM_MAP: Array<[string, PackageManager]> = [
  ['pnpm-lock.yaml', 'pnpm'],
  ['bun.lockb', 'bun'],
  ['bun.lock', 'bun'],
  ['yarn.lock', 'yarn'],
  ['package-lock.json', 'npm'],
]

const DEFAULT_PM_ORDER: PackageManager[] = ['pnpm', 'npm', 'bun', 'yarn']

async function detectPackageManager(cwd: string): Promise<PackageManager | null> {
  for (const [lockfile, pm] of LOCKFILE_PM_MAP) {
    if (await Bun.file(path.join(cwd, lockfile)).exists()) {
      return pm
    }
  }
  return null
}

export async function run(options: RunOptions = {}): Promise<void> {
  const cwd = options.cwd ? path.resolve(options.cwd) : process.cwd()
  const pkgPath = path.join(cwd, 'package.json')

  const pkgFile = Bun.file(pkgPath)
  if (!(await pkgFile.exists())) {
    console.error(ansis.red('No package.json found in current directory.'))
    process.exit(1)
  }

  let scripts: Record<string, string>
  try {
    const pkg = await pkgFile.json() as Record<string, unknown>
    if (!pkg.scripts || typeof pkg.scripts !== 'object' || Array.isArray(pkg.scripts)) {
      console.error(ansis.red('No scripts found in package.json.'))
      process.exit(1)
    }
    scripts = pkg.scripts as Record<string, string>
  }
  catch {
    console.error(ansis.red('Failed to parse package.json.'))
    process.exit(1)
  }

  const scriptEntries = Object.entries(scripts).filter(
    ([, value]) => typeof value === 'string' && value.trim() !== '',
  )

  if (scriptEntries.length === 0) {
    console.error(ansis.red('No runnable scripts found in package.json.'))
    process.exit(1)
  }

  printTitle()
  intro(ansis.bold('Run Script'))

  const selectedScript = await select({
    message: 'Select a script to run:',
    options: scriptEntries.map(([name, cmd]) => ({
      value: name,
      label: name,
      hint: cmd,
    })),
  })

  if (isCancel(selectedScript)) {
    cancel('Operation cancelled.')
    process.exit(0)
  }

  const detected = await detectPackageManager(cwd)
  const pmOrder: PackageManager[] = detected
    ? [detected, ...DEFAULT_PM_ORDER.filter(pm => pm !== detected)]
    : DEFAULT_PM_ORDER

  const selectedPm = await select({
    message: 'Select a package manager:',
    options: pmOrder.map(pm => ({ value: pm, label: pm })),
  })

  if (isCancel(selectedPm)) {
    cancel('Operation cancelled.')
    process.exit(0)
  }

  const cmd = [selectedPm, 'run', selectedScript]
  log.info(ansis.green(cmd.join(' ')))
  console.log() // Add an empty line for better readability

  if (options.passthroughArgs && options.passthroughArgs.length > 0) {
    cmd.push('--', ...options.passthroughArgs)
  }

  const proc = Bun.spawn(cmd, {
    cwd,
    stdin: 'inherit',
    stdout: 'inherit',
    stderr: 'inherit',
  })

  const exitCode = await proc.exited
  process.exit(exitCode)
}
