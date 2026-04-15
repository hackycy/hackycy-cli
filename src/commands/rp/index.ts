import type { Command } from 'commander'

export function register(program: Command): void {
  program
    .command('rp')
    .description('Run package.json scripts')
    .allowUnknownOption(true)
    .action(async (_options, cmd) => {
      const { rp } = await import('./rp')
      await rp({ passthroughArgs: cmd.args })
    })
}
