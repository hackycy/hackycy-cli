import type { Command } from 'commander'
import { parseIntArg } from '../../shared/utils'

function parsePort(value: string): number {
  if (!/^\d+$/.test(value))
    throw new Error(`'${value}' is not a valid port`)
  const port = parseIntArg(value)
  if (port > 65_535)
    throw new Error('Port must be between 0 and 65535')
  return port
}

export function register(program: Command): void {
  program
    .command('diff <baseline-directory> <target-directory>')
    .description('Compare two directories in a browser')
    .option('-p, --port <number>', 'Port to serve on', parsePort, 1205)
    .option('--public', 'Make the diff available on the local network')
    .option('-x, --exclude <glob>', 'Add an exclusion', (value, values: string[]) => [...values, value], [])
    .option('--no-gitignore', 'Do not apply Target Directory .gitignore files')
    .action(async (
      baselineDirectory: string,
      targetDirectory: string,
      options: { public: boolean, port: number, exclude: string[], gitignore: boolean },
    ) => {
      const { runDiffCommand } = await import('./run')
      await runDiffCommand({ baselineDirectory, targetDirectory, ...options })
    })
}
