import type { Command } from 'commander'
import type { CmOptions } from './types'

function parseTimeoutMs(value: string): number {
  const parsed = Number(value)
  if (!Number.isSafeInteger(parsed) || parsed < 1000)
    throw new Error(`'${value}' is not a valid timeout in milliseconds. Use an integer greater than or equal to 1000.`)
  return parsed
}

export function register(parent: Command): void {
  parent
    .command('cm')
    .description('Generate an Angular-style commit message from uncommitted changes')
    .option('--profile <name>', 'CM provider profile to use')
    .option('--timeout-ms <milliseconds>', 'Provider request timeout in milliseconds', parseTimeoutMs)
    .option('-l, --lang <lang>', 'Commit message language: en or zh', 'en')
    .option('-S, --staged', 'Only use staged changes')
    .option('-s, --stage', 'Select files to stage before generating')
    .option('-a, --stage-all', 'Stage all changes before generating')
    .option('-p, --push [remote]', 'Push to remote after creating the commit, defaults to origin')
    .option('-c, --stage-push [remote]', 'Select files to stage, commit, then push, defaults to origin')
    .option('-d, --dry-run', 'Generate and print only')
    .option('-b, --body', 'Allow a short commit body')
    .action(async (options: CmOptions) => {
      const { runGitCm } = await import('./run')
      await runGitCm(options)
    })
}
