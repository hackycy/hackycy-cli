import type { DidOptions } from './did'
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

cli.help()
cli.version(version)

cli.parse()
