import { describe, expect, test } from 'bun:test'
import { Command } from 'commander'
import { register } from './index'

describe('git cm command options', () => {
  test('keeps the supported command matrix', () => {
    const parent = new Command('git')
    register(parent)
    const command = parent.commands.find(item => item.name() === 'cm')

    expect(command?.options.map(option => option.long)).toEqual([
      '--profile',
      '--timeout-ms',
      '--lang',
      '--staged',
      '--stage',
      '--stage-all',
      '--push',
      '--stage-push',
      '--dry-run',
      '--body',
    ])
  })

  test('parses timeout overrides as integer milliseconds', () => {
    const parent = new Command('git')
    register(parent)
    const command = parent.commands.find(item => item.name() === 'cm')!

    command.parseOptions(['--timeout-ms', '123456'])

    expect(command.getOptionValue('timeoutMs')).toBe(123456)
  })

  test('rejects invalid timeout overrides', () => {
    const parent = new Command('git')
    register(parent)
    const command = parent.commands.find(item => item.name() === 'cm')!

    expect(() => command.parseOptions(['--timeout-ms', '999'])).toThrow('greater than or equal to 1000')
    expect(() => command.parseOptions(['--timeout-ms', '1.5'])).toThrow('valid timeout in milliseconds')
    expect(() => command.parseOptions(['--timeout-ms', 'not-a-number'])).toThrow('valid timeout in milliseconds')
  })
})
