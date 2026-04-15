import type { Command } from 'commander'
import type { DidOptions } from './types'
import { parseIntArg } from '../../shared/utils'

export function register(program: Command): void {
  program
    .command('did <directory>')
    .description('Did or Fish?')
    .option('--depth <number>', 'Find directory depth', parseIntArg, 5)
    .action(async (dir: string, options: DidOptions) => {
      const { findMyDid } = await import('./did')
      await findMyDid({
        root: dir,
        depth: options.depth,
      })
    })
}
