import { describe, expect, test } from 'bun:test'
import { Command } from 'commander'
import { register } from './index'

describe('tunnel command logging options', () => {
  test('leaves log level on the root command instead of tunnel subcommands', () => {
    const program = new Command()
    register(program)

    const tunnel = program.commands.find(command => command.name() === 'tunnel')!
    const server = tunnel.commands.find(command => command.name() === 'server')!
    const connect = tunnel.commands.find(command => command.name() === 'connect')!

    expect(server.options.some(option => option.long === '--log-level')).toBe(false)
    expect(connect.options.some(option => option.long === '--log-level')).toBe(false)
  })
})
