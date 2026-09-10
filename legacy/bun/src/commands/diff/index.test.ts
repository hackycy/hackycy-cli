import { describe, expect, test } from 'bun:test'
import { Command } from 'commander'
import { register } from './index'

describe('diff command registration', () => {
  test('uses --public for LAN sharing and preserves the port option', () => {
    const program = new Command()
    register(program)

    const help = program.commands[0]!.helpInformation()
    expect(help).toContain('-p, --port <number>')
    expect(help).toContain('--public')
    expect(help).not.toContain('--address')
    expect(help).not.toContain('-a,')
  })
})
