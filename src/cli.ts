import { cac } from 'cac'
import { version } from '../package.json'

const cli = cac('ycy')

// global options
interface GlobalCLIOptions {
  '--'?: string[]
}

cli
  .command('fish', 'Did nothing?')
  .option('-c, --config <file>', `[string] use specified config file`)
  .action(async (_options: GlobalCLIOptions) => {
    // TODO
  })

cli.help()
cli.version(version)

cli.parse()
