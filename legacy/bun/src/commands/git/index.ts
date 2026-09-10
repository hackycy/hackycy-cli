import type { Command } from 'commander'
import { register as registerCm } from './cm'
import { register as registerFork } from './fork'
import { register as registerHeat } from './heat'
import { register as registerPulse } from './pulse'

export function register(program: Command): void {
  const git = program.command('git').description('Git utilities')
  registerCm(git)
  registerHeat(git)
  registerPulse(git)
  registerFork(git)
}
