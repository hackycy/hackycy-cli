import type { Command } from 'commander'
import type { EnvOptions } from './types'

export function register(parent: Command): void {
  parent
    .command('env [dir]')
    .description('Export .env file contents as JSON')
    .option('-e, --env <name>', 'Environment name, skip interactive selection (e.g. local, prod)')
    .option('-o, --out <file>', 'Write output to file instead of stdout')
    .action(async (dir: string | undefined, options: Omit<EnvOptions, 'dir'>) => {
      const { exportEnv } = await import('./env')
      await exportEnv({ dir, ...options })
    })
}
