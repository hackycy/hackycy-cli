import type { Command } from 'commander'
import { register as registerEnv } from './env'

export function register(program: Command): void {
  const exportCmd = program.command('export').description('Export utilities')
  registerEnv(exportCmd)
}
