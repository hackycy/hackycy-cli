import process from 'node:process'
import { Command } from 'commander'
import { version } from '../package.json'
import { register as registerExport } from './commands/export'
import { register as registerGit } from './commands/git'
import { register as registerRm } from './commands/rm'
import { register as registerRun } from './commands/run'
import { register as registerServe } from './commands/serve'
import { register as registerUpgrade } from './commands/upgrade'
import { register as registerZip } from './commands/zip'

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

registerExport(program)
registerGit(program)
registerRm(program)
registerServe(program)
registerZip(program)
registerRun(program)
registerUpgrade(program)

program.on('command:*', (operands) => {
  console.error(`error: unknown command '${operands[0]}'`)
  process.exit(1)
})

program.parse()
