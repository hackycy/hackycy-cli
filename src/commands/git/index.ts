import type { Command } from 'commander'
import { register as registerConfig } from './config'
import { register as registerFork } from './fork'
import { register as registerLs } from './ls'

export function register(program: Command): void {
  const git = program.command('git').description('Git utilities')
  registerLs(git)
  registerFork(git)
  registerConfig(git)
}
