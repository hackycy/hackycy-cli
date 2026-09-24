import { afterEach, expect, it, vi } from 'vitest'
import { apiJson } from './api'

afterEach(() => vi.unstubAllGlobals())

it('preserves the server error code and safe details for Node actions', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
    version: 1,
    error: { code: 'NODE_IDENTITY_MISMATCH', message: 'Node identity changed', details: { nodeId: 'node-1' } },
  }), { status: 409, headers: { 'Content-Type': 'application/json' } })))

  await expect(apiJson('/api/nodes')).rejects.toMatchObject({
    status: 409,
    code: 'NODE_IDENTITY_MISMATCH',
    message: 'Node identity changed',
    details: { nodeId: 'node-1' },
  })
})
