import type { ResolvedCmProfile } from '../../../config/types'
import { stripVTControlCharacters } from 'node:util'
import { describe, expect, test } from 'bun:test'
import { formatGeneratedMessage } from './run'

const profile: ResolvedCmProfile = {
  name: 'deepseek',
  baseURL: 'https://api.deepseek.com',
  model: 'deepseek-chat',
  apiKey: 'test-key',
  temperature: 0.2,
  timeoutMs: 30_000,
  maxOutputTokens: 200,
}

const completeEvidence = {
  estimatedLocalPromptTokens: 456,
  representedClusters: 1,
  totalClusters: 1,
  includedFacts: 4,
  omittedFacts: 0,
  contentCompacted: false,
}

describe('formatGeneratedMessage', () => {
  test('prints a compact result without changed file details', () => {
    const output = stripVTControlCharacters(formatGeneratedMessage(
      'feat(cm): streamline generated output',
      profile,
      {
        promptTokens: 1_234,
        completionTokens: 42,
        totalTokens: 1_276,
      },
      completeEvidence,
    ))

    expect(output).toBe([
      'feat(cm): streamline generated output',
      '',
      'Profile: deepseek (deepseek-chat)',
      'Provider tokens: 1,234 prompt / 42 completion / 1,276 total',
      'Local evidence estimate: ~456 serialized prompt tokens / 1 of 1 clusters / 4 of 4 facts',
    ].join('\n'))
    expect(output).not.toContain('Changed files')
    expect(output).not.toContain('compacted semantic evidence')
  })

  test('preserves a multiline commit body and handles unavailable usage', () => {
    const output = stripVTControlCharacters(formatGeneratedMessage(
      'fix(cm): preserve commit body\n\nKeep the generated context intact.',
      profile,
      undefined,
      completeEvidence,
    ))

    expect(output).toContain('fix(cm): preserve commit body\n\nKeep the generated context intact.')
    expect(output).toContain('Provider tokens: unavailable')
  })

  test('shows unknown partial usage values and semantic evidence compaction', () => {
    const output = stripVTControlCharacters(formatGeneratedMessage(
      'feat(cm): streamline generated output',
      profile,
      { completionTokens: 42 },
      {
        estimatedLocalPromptTokens: 2_000,
        representedClusters: 2,
        totalClusters: 3,
        includedFacts: 18,
        omittedFacts: 13,
        contentCompacted: true,
      },
    ))

    expect(output).toContain('Provider tokens: unknown prompt / 42 completion / unknown total')
    expect(output).toContain('Local evidence estimate: ~2,000 serialized prompt tokens / 2 of 3 clusters / 18 of 31 facts')
    expect(output).toContain('Commit scope: 3 clusters represented with compacted semantic evidence.')
    expect(output).toContain('This does not affect which files are committed.')
  })
})
