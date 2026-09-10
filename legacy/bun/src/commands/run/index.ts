import type { Command } from 'commander'

export function register(program: Command): void {
  program
    .command('run [path]')
    .description('Run package.json scripts')
    .allowUnknownOption(true)
    .action(async (cwdArg, _options, cmd) => {
      const { run } = await import('./run')
      const passthroughArgs = cwdArg ? cmd.args.slice(1) : cmd.args
      await run({ passthroughArgs, cwd: cwdArg })
    })
}
