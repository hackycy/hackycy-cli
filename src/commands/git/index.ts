import type { Command } from 'commander'
import { register as registerLs } from './ls'

export function register(program: Command): void {
  const git = program.command('git').description('Git utilities')
  registerLs(git)
}
