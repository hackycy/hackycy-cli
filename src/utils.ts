import process from 'node:process'
import readline from 'node:readline'
import ansis from 'ansis'

export function clearScreen(): void {
  const repeatCount = process.stdout.rows - 2
  const blank = repeatCount > 0 ? '\n'.repeat(repeatCount) : ''
  console.log(blank)
  readline.cursorTo(process.stdout, 0, 0)
  readline.clearScreenDown(process.stdout)
}

export function printTitle(): void {
  clearScreen()
  console.log(ansis.bold.cyanBright('HACKYCY CLI'))
  console.log()
}
