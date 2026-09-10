import { describe, expect, test } from 'bun:test'
import { TunnelDatabase } from './database'

describe('TunnelDatabase schema', () => {
  test('creates the fresh account and ownership schema', () => {
    const database = new TunnelDatabase(':memory:')
    const tables = database.sqlite.query<{ name: string }, []>('SELECT name FROM sqlite_master WHERE type = \'table\' ORDER BY name').all().map(row => row.name)
    const clientColumns = database.sqlite.query<{ name: string }, []>('PRAGMA table_info(clients)').all().map(row => row.name)
    const tunnelColumns = database.sqlite.query<{ name: string }, []>('PRAGMA table_info(tunnels)').all().map(row => row.name)
    expect(tables).toContain('accounts')
    expect(tables).toContain('tunnel_http_routes')
    expect(clientColumns).toContain('owner_account_id')
    expect(tunnelColumns).toContain('location')
    expect(tunnelColumns).toContain('options_json')
    expect(database.sqlite.query<{ value: string }, []>('SELECT value FROM meta WHERE key = \'schema_version\'').get()).toEqual({ value: '1' })
    database.close()
  })
})
