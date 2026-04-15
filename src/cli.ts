import type { DidOptions } from './did'
import type { ServeOptions } from './serve'
import process from 'node:process'
import { Command } from 'commander'
import { version } from '../package.json'

function parseIntArg(value: string): number {
  const parsed = Number.parseInt(value, 10)
  if (Number.isNaN(parsed))
    throw new Error(`'${value}' is not a valid integer`)
  return parsed
}

function errorHandler(error: Error): void {
  let message = error.message || String(error)

  if (process.env.DEBUG || process.env.NODE_ENV === 'development')
    message += `\n\n${error.stack || ''}`

  console.log()
  console.error(message)
  process.exit(1)
}

process.on('uncaughtException', errorHandler)
process.on('unhandledRejection', errorHandler)

const program = new Command()
  .name('ycy')
  .version(version)

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

program
  .command('serve <directory>')
  .description('Serve static files from a directory')
  .option('-p, --port <number>', 'Port to serve on', parseIntArg, 1204)
  .option('-a, --address <string>', 'Address to bind to', '0.0.0.0')
  .action(async (directory: string, options: Omit<ServeOptions, 'directory'>) => {
    const { serve } = await import('./serve')
    await serve({
      directory,
      ...options,
    })
  })

program
  .command('zip <directory>')
  .description('Zip a directory into a zip file')
  .option('-w, --without-open', 'Do not open the zip file after creation')
  .option('-d, --with-dir <dir>', 'Include the directory name as a top-level folder in the zip')
  .action(async (directory: string, options: { withoutOpen?: boolean, withDir?: string }) => {
    const { zip } = await import('./zip')
    await zip({
      directory,
      open: !options.withoutOpen,
      withDir: options.withDir,
    })
  })

program
  .command('rp')
  .description('Run package.json scripts')
  .allowUnknownOption(true)
  .action(async (_options, cmd) => {
    const { rp } = await import('./rp')
    await rp({ passthroughArgs: cmd.args })
  })

program
  .command('upgrade')
  .description('Upgrade cli to the latest version')
  .action(async () => {
    const { upgradeCli } = await import('./upgrade')
    await upgradeCli()
  })

program.on('command:*', (operands) => {
  console.error(`error: unknown command '${operands[0]}'`)
  process.exit(1)
})

program.parse()
