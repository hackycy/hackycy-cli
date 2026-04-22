import type { Command } from 'commander'

export function register(program: Command): void {
  program
    .command('run')
    .description('Run package.json scripts')
    .allowUnknownOption(true)
    .action(async (_options, cmd) => {
      const { run } = await import('./run')
      await run({ passthroughArgs: cmd.args })
    })
}
