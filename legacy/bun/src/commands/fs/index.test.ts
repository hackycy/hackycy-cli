import { describe, expect, test } from 'bun:test'
import { Command } from 'commander'
import { register } from './index'

describe('fs command registration', () => {
  test('makes the directory optional and documents its default', () => {
    const program = new Command()
    register(program)

    const command = program.commands[0]!
    expect(command.name()).toBe('fs')
    expect(program.commands.some(candidate => candidate.name() === 'serve')).toBe(false)
    expect(command.usage()).toBe('[options] [directory]')
    expect(command.description()).toContain('defaults to current directory')
  })

  test('requires an explicit management flag for filesystem mutations', () => {
    const program = new Command()
    register(program)

    const command = program.commands[0]!
    expect(command.options.map(option => option.long)).toContain('--manage')
    expect(command.options.map(option => option.long)).not.toContain('--upload')
    expect(command.getOptionValue('manage')).toBe(false)
  })

  test('keeps chunked uploads opt-in and exposes a bounded chunk size option', () => {
    const program = new Command()
    register(program)

    const command = program.commands[0]!
    expect(command.options.map(option => option.long)).toContain('--chunked-upload')
    expect(command.options.map(option => option.long)).toContain('--upload-chunk-size')
    expect(command.getOptionValue('chunkedUpload')).toBeUndefined()
  })

  test('keeps safe HTML mode opt-in', () => {
    const program = new Command()
    register(program)

    const command = program.commands[0]!
    expect(command.options.map(option => option.long)).toContain('--safe-html')
    expect(command.getOptionValue('safeHtml')).toBe(false)
  })

  test('collects repeated accounts without enabling authentication by default', () => {
    const program = new Command()
    register(program)

    const command = program.commands[0]!
    expect(command.getOptionValue('account')).toEqual([])

    command.parseOptions([
      '--account',
      'alice:password123',
      '--account',
      'bob:password456',
    ])
    expect(command.getOptionValue('account')).toEqual([
      'alice:password123',
      'bob:password456',
    ])
  })
})
