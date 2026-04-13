import type { DidOptions } from './did'
import type { ServeOptions } from './serve'
import process from 'node:process'
import { cac } from 'cac'
import { version } from '../package.json'

const cli = cac('ycy')

// global options
interface GlobalCLIOptions {
  '--'?: string[]
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

cli
  .command('did <directory>', 'Did or Fish?')
  .option('--depth <number>', 'Find directory depth', { default: 5 })
  .action(async (dir: string, options: GlobalCLIOptions & DidOptions) => {
    const { findMyDid } = await import('./did')
    await findMyDid({
      root: dir,
      depth: options.depth,
    })
  })

cli
  .command('serve <directory>', 'Serve static files from a directory')
  .option('-p, --port <number>', 'Port to serve on', { default: 1204 })
  .option('-a, --address <string>', 'Address to bind to', { default: '0.0.0.0' })
  .action(async (directory: string, options: GlobalCLIOptions & Omit<ServeOptions, 'directory'>) => {
    const { serve } = await import('./serve')
    await serve({
      directory,
      ...options,
    })
  })

cli
  .command('zip <directory>', 'Zip a directory into a zip file')
  .option('-w, --without-open', 'Do not open the zip file after creation')
  .option('-d, --with-dir <dir>', 'Include the directory name as a top-level folder in the zip')
  .action(async (directory: string, options: GlobalCLIOptions & { withoutOpen?: boolean, withDir?: string }) => {
    const { zip } = await import('./zip')
    await zip({
      directory,
      open: !options.withoutOpen,
      withDir: options.withDir,
    })
  })

cli
  .command('rp', 'Run package.json scripts')
  .action(async (options: GlobalCLIOptions) => {
    const { rp } = await import('./rp')
    await rp({ passthroughArgs: options['--'] ?? [] })
  })

cli
  .command('upgrade', 'Upgrade cli to the latest version')
  .action(async () => {
    const { upgradeCli } = await import('./upgrade')
    await upgradeCli()
  })

// fallback command for unknown commands
cli
  .command('')
  .action(() => {
    cli.outputHelp()
    process.exit(0)
  })

cli.help()
cli.version(version)

cli.parse()
