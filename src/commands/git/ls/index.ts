import type { Command } from 'commander'
import type { GitLsOptions } from './types'
import { parseIntArg } from '../../../shared/utils'

export function register(parent: Command): void {
  parent
    .command('ls <directory>')
    .description('List recent git commits across repositories')
    .option('--days <number>', 'Number of days to search', parseIntArg)
    .action(async (directory: string, options: GitLsOptions) => {
      const { runGitLs } = await import('./ls')
      await runGitLs(directory, options)
    })
}
