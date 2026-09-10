import type { ResolvedCmProfile } from './types'
import { afterEach, describe, expect, test } from 'bun:test'
import { createChatCompletionWithUsage } from './client'

const profile: ResolvedCmProfile = {
  name: 'test',
  baseURL: 'https://provider.test',
  model: 'test-model',
  apiKey: 'test-key',
  temperature: 0.2,
  timeoutMs: 30_000,
  maxOutputTokens: 321,
}

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
})

describe('createChatCompletionWithUsage', () => {
  test('keeps the profile output limit unless a request override is supplied', async () => {
    const requests: Array<{ max_tokens?: number }> = []
    globalThis.fetch = (async (_input, init) => {
      requests.push(JSON.parse(String(init?.body)) as { max_tokens?: number })
      return new Response(JSON.stringify({ choices: [{ message: { content: 'ok' } }] }))
    }) as typeof fetch
    const messages = [{ role: 'system' as const, content: 'Return ok' }]

    await createChatCompletionWithUsage(profile, messages)
    await createChatCompletionWithUsage(profile, messages, { maxOutputTokens: 80 })

    expect(requests.map(request => request.max_tokens)).toEqual([321, 80])
  })

  test('reports the effective timeout when the provider request is aborted', async () => {
    globalThis.fetch = (async (_input: string | URL | Request, init: RequestInit | undefined) => {
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          const error = new Error('aborted')
          error.name = 'AbortError'
          reject(error)
        }, { once: true })
      })
    }) as unknown as typeof fetch

    await expect(createChatCompletionWithUsage(
      { ...profile, timeoutMs: 1_000 },
      [{ role: 'system', content: 'Return ok' }],
    )).rejects.toThrow('Provider request timed out after 1000ms')
  })
})
