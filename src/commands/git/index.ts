import type { Command } from 'commander'
import { register as registerAct } from './act'
import { register as registerConfig } from './config'
import { register as registerFork } from './fork'

export function register(program: Command): void {
  const git = program.command('git').description('Git utilities')
  registerAct(git)
  registerFork(git)
  registerConfig(git)
}
