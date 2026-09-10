import type { Command } from 'commander'
import type { GitLsOptions } from './types'
import process from 'node:process'
import { parseIntArg } from '../../../shared/utils'

export function register(parent: Command): void {
  parent
    .command('pulse [directory]')
    .description('Show recent git activity across repositories')
    .option('--days <number>', 'Number of days to search', parseIntArg)
    .action(async (directory: string | undefined, options: GitLsOptions) => {
      const { runGitPulse } = await import('./pulse')
      await runGitPulse(directory ?? process.cwd(), options)
    })
}
