import type { DiffHunk, GitChangeSnapshot, SnapshotFile } from './types'
import { describe, expect, test } from 'bun:test'
import {
  buildChangeClusters,
  compileEvidence,
  estimateLocalPromptTokens,
  extractEvidenceFacts,
  MAX_LOCAL_PROMPT_TOKENS,
} from './evidence'

const system = 'Return a concise Angular commit message.'

function hunk(addedLines: string[], deletedLines: string[] = []): DiffHunk {
  return {
    id: 'worktree:example:0',
    source: 'worktree',
    oldStart: 1,
    oldLines: deletedLines.length,
    newStart: 1,
    newLines: addedLines.length,
    addedLines,
    deletedLines,
  }
}

function file(overrides: Partial<SnapshotFile> & Pick<SnapshotFile, 'path'>): SnapshotFile {
  return {
    id: overrides.path,
    status: `M ${overrides.path}`,
    indexStatus: ' ',
    worktreeStatus: 'M',
    role: 'source',
    contentPolicy: 'inspect',
    stats: { additions: 1, deletions: 0 },
    hunks: [hunk(['export const value = true'])],
    ...overrides,
  }
}

function snapshot(files: SnapshotFile[]): GitChangeSnapshot {
  return {
    repoRoot: '/repo',
    scope: 'all-uncommitted',
    snapshotId: 'snapshot',
    files,
    totals: files.reduce((total, item) => ({
      additions: total.additions + item.stats.additions,
      deletions: total.deletions + item.stats.deletions,
    }), { additions: 0, deletions: 0 }),
  }
}

describe('semantic evidence', () => {
  test('is byte-stable and preserves the changed directory hierarchy in P0', () => {
    const files = [
      file({ path: 'src/commands/cm/engine.ts' }),
      file({ path: 'src/commands/cm/engine.test.ts', role: 'test' }),
      file({ path: 'packages/web/src/view.ts' }),
      file({ path: 'docs/cm.md', role: 'docs' }),
      file({ path: 'package.json', role: 'dependency' }),
    ]
    const first = compileEvidence(snapshot(files), system)
    const second = compileEvidence(snapshot([...files].reverse()), system)

    expect(first.text).toBe(second.text)
    expect(first.coverage).toEqual(second.coverage)
    expect(first.text).toContain('DIRECTORY_CONTEXT')
    expect(first.text).toContain('CHANGE_SUMMARY files=5 +5 -0 protected=0/0')
    expect(first.text).toContain('./ files=1 roles=dependency:1 +1 -0')
    expect(first.text).toContain('      cm/ files=2 roles=source:1,test:1 +2 -0')
    expect(first.text).toContain('      src/ files=1 roles=source:1 +1 -0')
    expect(first.text).not.toContain('cluster=')
    expect(first.text).not.toContain('COMMIT_EVIDENCE')
    expect(first.coverage.representedClusters).toBe(first.coverage.totalClusters)
  })

  test('keeps a deep changed directory intact instead of using a shortened cluster root', () => {
    const compiled = compileEvidence(snapshot([
      file({ path: 'src/commands/git/cm/engine.ts' }),
      file({ path: 'src/commands/git/cm/run.ts' }),
    ]), system)

    expect(compiled.text).toContain('DIRECTORY_CONTEXT')
    expect(compiled.text).toContain('        cm/ files=2 roles=source:2 +2 -0')
    expect(compiled.text).not.toContain('cluster=src/commands/git')
  })

  test('groups multiple facts under one directory and file path', () => {
    const compiled = compileEvidence(snapshot([
      file({
        path: 'src/commands/git/cm/engine.ts',
        hunks: [hunk([
          'export function buildEvidence(): void {}',
          'throw new Error(\'Evidence is unavailable\')',
          'const compact = true',
        ])],
      }),
    ]), system)

    const facts = compiled.text.slice(compiled.text.indexOf('FACTS'))
    expect(facts).toContain('src/commands/git/cm/')
    expect(facts).toContain('  engine.ts:')
    expect(facts).toContain('symbol added buildEvidence')
    expect(facts).toContain('added throw new Error(\'Evidence is unavailable\')')
    expect(facts.match(/engine\.ts:/g)).toHaveLength(1)
    expect(facts).not.toContain('src/commands/git/cm/engine.ts:')

    const p0 = compiled.text.slice(0, compiled.text.indexOf('FACTS')).trimEnd()
    const repeatedPaths = [
      p0,
      ...compiled.facts.map(item => `  ${item.filePath}: ${item.text}`),
    ].join('\n')
    expect(estimateLocalPromptTokens(system, compiled.text)).toBeLessThan(estimateLocalPromptTokens(system, repeatedPaths))
  })

  test('records both sides of an inspectable rename without adding protected directories', () => {
    const renamed = file({
      path: 'src/new/location.ts',
      originalPath: 'src/old/location.ts',
      indexStatus: 'R',
      stats: { additions: 3, deletions: 1 },
    })
    const sensitive = file({
      path: 'private/.env.production',
      role: 'sensitive',
      contentPolicy: 'redacted',
    })
    const generated = file({
      path: 'dist/app.js',
      role: 'generated',
      contentPolicy: 'metadata-only',
    })
    const compiled = compileEvidence(snapshot([renamed, sensitive, generated]), system)

    expect(compiled.text).toContain('old/ rename-from=1 rename-to=0')
    expect(compiled.text).toContain('new/ files=1 roles=source:1 +3 -1 rename-from=0 rename-to=1')
    expect(compiled.text).toContain('location.ts:\n    rename from src/old/location.ts')
    expect(compiled.text).not.toContain('private/')
    expect(compiled.text).not.toContain('dist/')
  })

  test('compacts scattered directories while retaining an atomic directory context within budget', () => {
    const files = Array.from({ length: 120 }, (_, index) => file({
      path: `packages/module-${index}/src/deep/feature-${index}.ts`,
    }))
    const first = compileEvidence(snapshot(files), system)
    const second = compileEvidence(snapshot([...files].reverse()), system)

    expect(first.text).toBe(second.text)
    expect(first.coverage.contentCompacted).toBe(true)
    expect(first.text).toContain('DIRECTORY_CONTEXT')
    expect(first.text).toMatch(/packages\/ files=\d+ roles=source:\d+ \+\d+ -0/)
    expect(first.coverage.estimatedLocalPromptTokens).toBe(estimateLocalPromptTokens(system, first.text))
    expect(estimateLocalPromptTokens(system, first.text)).toBeLessThanOrEqual(MAX_LOCAL_PROMPT_TOKENS)
  })

  test('compacts low-weight branches before the primary source directory', () => {
    const sourceFiles = Array.from({ length: 20 }, (_, index) => file({
      path: `src/payments/core/handler-${index}.ts`,
      stats: { additions: 20, deletions: 0 },
    }))
    const documentationFiles = Array.from({ length: 120 }, (_, index) => file({
      path: `docs/guides/topic-${index}/README.md`,
      role: 'docs',
    }))
    const compiled = compileEvidence(snapshot([...sourceFiles, ...documentationFiles]), system)

    expect(compiled.coverage.contentCompacted).toBe(true)
    expect(compiled.text).toContain('      core/ files=20 roles=source:20 +400 -0')
    expect(estimateLocalPromptTokens(system, compiled.text)).toBeLessThanOrEqual(MAX_LOCAL_PROMPT_TOKENS)
  })

  test('extracts package, declaration, test, behavior, and import facts by priority', () => {
    const packageFile = file({
      path: 'package.json',
      role: 'dependency',
      manifest: {
        before: JSON.stringify({ dependencies: { obsolete: '1.0.0', zod: '4.0.0' }, scripts: { test: 'bun test' } }),
        after: JSON.stringify({ dependencies: { zod: '4.4.3', vitest: '3.0.0' }, scripts: { test: 'bun test', lint: 'eslint .' } }),
      },
      hunks: [hunk([])],
    })
    const sourceFile = file({
      path: 'src/feature.ts',
      hunks: [hunk([
        'import { value } from \'./value\'',
        'export function enableFeature(): void {}',
        'throw new Error(\'Feature is unavailable\')',
        'const internal = value',
      ])],
    })
    const testFile = file({
      path: 'src/feature.test.ts',
      role: 'test',
      hunks: [hunk(['test(\'enables the feature\', () => expect(true).toBe(true))'])],
    })
    const facts = extractEvidenceFacts(buildChangeClusters(snapshot([packageFile, sourceFile, testFile])))

    expect(facts.some(item => item.priority === 1 && item.text.includes('dependency replacement add vitest@3.0.0; remove obsolete@1.0.0'))).toBe(true)
    expect(facts.some(item => item.priority === 1 && item.text.includes('symbol added enableFeature'))).toBe(true)
    expect(facts.some(item => item.priority === 1 && item.text.includes('test added "enables the feature"'))).toBe(true)
    expect(facts.some(item => item.priority === 2 && item.text.includes('Feature is unavailable'))).toBe(true)
    expect(facts.some(item => item.priority === 3 && item.text.startsWith('added const internal'))).toBe(true)
  })

  test('extracts a configuration key when a file has only low-priority implementation lines', () => {
    const configFile = file({
      path: 'tsconfig.json',
      role: 'config',
      hunks: [hunk(['    "types": ["bun", "./types.d.ts"],'])],
    })
    const facts = extractEvidenceFacts(buildChangeClusters(snapshot([configFile])))

    expect(facts).toContainEqual(expect.objectContaining({ priority: 1, text: 'config added types' }))
  })

  test('adds a deterministic type hint for workflow, Dockerfile, TypeScript config, docs, style, and release-only changes', () => {
    const cases: Array<[SnapshotFile, string]> = [
      [file({ path: '.github/workflows/release.yml', role: 'config' }), 'type=ci'],
      [file({ path: 'Dockerfile', role: 'unknown' }), 'type=build'],
      [file({ path: 'README.md', role: 'docs' }), 'type=docs'],
      [file({ path: 'styles.css', role: 'unknown' }), 'type=style'],
      [file({
        path: 'package.json',
        role: 'dependency',
        manifest: { before: JSON.stringify({ version: '1.0.0' }), after: JSON.stringify({ version: '1.0.1' }) },
      }), 'type=chore'],
    ]

    for (const [changedFile, expected] of cases)
      expect(compileEvidence(snapshot([changedFile]), system).text).toContain(expected)

    expect(compileEvidence(snapshot([
      file({ path: 'tsconfig.json', role: 'config' }),
      file({ path: 'types.d.ts', role: 'config' }),
    ]), system).text).toContain('type=build')
  })

  test('stays within the complete-message budget without truncating a fact or protected content', () => {
    const oversizedLine = `throw new Error('${'x'.repeat(8_000)}')`
    const publicFile = file({ path: 'src/public.ts', hunks: [hunk([oversizedLine, 'export const enabled = true'])] })
    const sensitiveFile = file({
      path: '.env',
      role: 'sensitive',
      contentPolicy: 'redacted',
      hunks: [hunk(['API_KEY=never-send-this'])],
    })
    const compiled = compileEvidence(snapshot([publicFile, sensitiveFile]), system)

    expect(estimateLocalPromptTokens(system, compiled.text)).toBeLessThanOrEqual(MAX_LOCAL_PROMPT_TOKENS)
    expect(compiled.text).not.toContain('never-send-this')
    expect(compiled.text).not.toContain('x'.repeat(100))
    expect(compiled.coverage.omittedFacts).toBeGreaterThan(0)
    expect(compiled.text).toContain('symbol added enabled')
  })
})
