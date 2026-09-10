import { stringify } from 'smol-toml'

export type TomlScalar = boolean | number | string
export type TomlValue = TomlDocument | TomlScalar | TomlValue[]

export interface TomlDocument {
  [key: string]: TomlValue | undefined
}

export interface TomlCodec {
  parse: (source: string) => TomlDocument
  stringify: (document: TomlDocument) => string
}

function unsupportedValue(path: string, value: unknown): never {
  const kind = value === null ? 'null' : Array.isArray(value) ? 'array' : typeof value
  throw new TypeError(`TOML document contains unsupported ${kind} at ${path}`)
}

function isTomlTable(value: unknown): value is Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    return false
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

function validateTomlValue(value: unknown, path: string, ancestors: Set<object>): void {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean')
    return
  if (!value || typeof value !== 'object')
    unsupportedValue(path, value)
  if (ancestors.has(value))
    throw new TypeError(`TOML document contains a circular reference at ${path}`)

  if (!Array.isArray(value) && !isTomlTable(value))
    unsupportedValue(path, value)

  ancestors.add(value)
  for (const [key, nested] of Object.entries(value))
    validateTomlValue(nested, Array.isArray(value) ? `${path}[${key}]` : `${path}.${key}`, ancestors)
  ancestors.delete(value)
}

function validateTomlDocument(document: TomlDocument): void {
  if (!isTomlTable(document))
    unsupportedValue('document', document)
  validateTomlValue(document, 'document', new Set())
}

function parseToml(source: string): TomlDocument {
  const document = Bun.TOML.parse(source)
  if (!isTomlTable(document))
    throw new TypeError('TOML parser returned a non-table document')
  return document as TomlDocument
}

export const tomlCodec: TomlCodec = {
  parse: parseToml,
  stringify(document) {
    validateTomlDocument(document)
    const rendered = stringify(document)
    const source = rendered.endsWith('\n') ? rendered : `${rendered}\n`
    tomlCodec.parse(source)
    return source
  },
}
