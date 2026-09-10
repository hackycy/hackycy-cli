import process from 'node:process'
import { Command } from 'commander'
import { version } from '../package.json'
import { register as registerConfig } from './commands/config'
import { register as registerDiff } from './commands/diff'
import { register as registerExport } from './commands/export'
import { register as registerFs } from './commands/fs'
import { register as registerGit } from './commands/git'
import { register as registerRm } from './commands/rm'
import { register as registerRun } from './commands/run'
import { register as registerTunnel } from './commands/tunnel'
import { register as registerUpgrade } from './commands/upgrade'
import { consumeUpdateState, formatUpdateState, INTERNAL_UPDATE_COMMAND, INTERNAL_UPDATE_VERIFY_ENV, runInternalUpdater } from './commands/upgrade/updater'
import { register as registerZip } from './commands/zip'
import { configureLogger, resolveLogLevel } from './shared/log'

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

const internalUpdateIndex = process.argv.indexOf(INTERNAL_UPDATE_COMMAND)

if (internalUpdateIndex !== -1) {
  try {
    await runInternalUpdater(process.argv.slice(internalUpdateIndex + 1))
  }
  catch {
    process.exitCode = 1
  }
}
else {
  const updateState = process.env[INTERNAL_UPDATE_VERIFY_ENV] === '1'
    ? null
    : consumeUpdateState(process.execPath)
  if (updateState?.status === 'pending') {
    console.error(formatUpdateState(updateState))
    process.exitCode = 1
  }
  else {
    if (updateState) {
      console.log(formatUpdateState(updateState))
    }

    const program = new Command()
      .name('ycy')
      .version(version)
      .option('--log-level <level>', 'Log level: debug, info, warn, or error')

    program.hook('preAction', () => {
      configureLogger({ level: resolveLogLevel(program.opts().logLevel) })
    })

    registerExport(program)
    registerDiff(program)
    registerConfig(program)
    registerGit(program)
    registerRm(program)
    registerFs(program)
    registerTunnel(program)
    registerZip(program)
    registerRun(program)
    registerUpgrade(program)

    program.on('command:*', (operands) => {
      console.error(`error: unknown command '${operands[0]}'`)
      process.exit(1)
    })

    program.parse()
  }
}
