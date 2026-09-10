import type { ResolvedCmProfile } from '../../../config/types'
import { afterEach, describe, expect, test } from 'bun:test'
import { createOpenAICompatibleCommitMessageModel } from './model'

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

describe('OpenAI-compatible commit message model', () => {
  test('uses the hard-coded commit-message output budget instead of profile configuration', async () => {
    let request: { max_tokens?: number } | undefined
    globalThis.fetch = (async (_input, init) => {
      request = JSON.parse(String(init?.body)) as { max_tokens?: number }
      return new Response(JSON.stringify({ choices: [{ message: { content: 'feat(scope): subject' } }] }))
    }) as typeof fetch

    const result = await createOpenAICompatibleCommitMessageModel(profile).generate({
      system: 'Return a commit message.',
      evidence: 'DIRECTORY_CONTEXT\nsrc/feature/',
    })

    expect(result.content).toBe('feat(scope): subject')
    expect(request?.max_tokens).toBe(4_096)
  })
})
