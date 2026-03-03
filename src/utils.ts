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

/*
 * A hyperlink is opened upon encountering an OSC 8 escape sequence with the target URI. The syntax is
 * OSC 8 ; params ; URI BEL|ST
 *
 * Following this, all subsequent cells that are painted are hyperlinks to this target.
 * A hyperlink is closed with the same escape sequence, omitting the parameters and the URI but keeping the separators:
 *
 * OSC 8 ; ; BEL|ST
 * const ST = '\u001B\\';
 */
export function hyperlinker(text: string, uri?: string): string {
  uri = uri || text

  // const ESC = '\u001B['
  const OSC = '\u001B]'
  const BEL = '\u0007'
  const SEP = ';'

  return [OSC, '8', SEP, SEP, uri, BEL, text, OSC, '8', SEP, SEP, BEL].join('')
}
