import type { TomlDocument } from './toml'
import { describe, expect, test } from 'bun:test'
import { tomlCodec } from './toml'

describe('TOML codec', () => {
  test('round-trips tables, arrays of tables, dynamic keys, and escaped strings', () => {
    const document: TomlDocument = {
      title: 'quote " and slash \\',
      auth: { method: 'token', token: 'secret' },
      headers: { set: Object.fromEntries([['X.Test', 'quote " and slash \\']]) },
      proxies: [{ name: 'first', ports: [7000, 7001] }, { name: 'second' }],
    }
    const source = tomlCodec.stringify(document)

    expect(source.endsWith('\n')).toBe(true)
    expect(tomlCodec.parse(source)).toEqual(document)
  })

  test('reports invalid TOML source', () => {
    expect(() => tomlCodec.parse('value = =')).toThrow('Unexpected =')
  })

  test('rejects values outside the codec document contract', () => {
    const circular: Record<string, unknown> = {}
    circular.self = circular

    for (const document of [
      { value: null },
      { value: undefined },
      { value: 1n },
      { value: () => {} },
      { value: new Date() },
      circular,
    ]) {
      expect(() => tomlCodec.stringify(document as TomlDocument)).toThrow(TypeError)
    }
  })
})
