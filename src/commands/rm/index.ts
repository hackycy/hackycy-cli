import type { Command } from 'commander'
import { parseIntArg } from '../../shared/utils'

export function register(program: Command): void {
  program
    .command('rm [paths...]')
    .description('Remove files/dirs, or smartly clean project artifacts when no path given')
    .option('-f, --force', 'Skip confirmation prompt')
    .option('-d, --depth <n>', 'Smart scan depth (default: 5)', parseIntArg)
    .action(async (paths: string[], options: { force?: boolean, depth?: number }) => {
      const { rm } = await import('./rm')
      await rm(paths, options)
    })
}
