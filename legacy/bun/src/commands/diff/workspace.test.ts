import { rmSync, writeFileSync } from 'node:fs'
import { chmod, mkdir, mkdtemp, rm, symlink, utimes, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { createComparisonWorkspace } from './workspace'

const temporaryDirectories: string[] = []

async function makeComparisonDirectories(): Promise<{ baseline: string, target: string }> {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-diff-'))
  temporaryDirectories.push(root)
  const baseline = path.join(root, 'baseline')
  const target = path.join(root, 'target')
  await Promise.all([
    mkdir(baseline),
    mkdir(target),
  ])
  return { baseline, target }
}

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

describe('ComparisonWorkspace', () => {
  test('publishes all four comparison statuses for exact paths', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'deleted.txt'), 'baseline only'),
      writeFile(path.join(baseline, 'modified.txt'), 'before'),
      writeFile(path.join(baseline, 'unchanged.txt'), 'same bytes'),
      writeFile(path.join(target, 'added.txt'), 'target only'),
      writeFile(path.join(target, 'modified.txt'), 'after'),
      writeFile(path.join(target, 'unchanged.txt'), 'same bytes'),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({ includeUnchanged: true }).entries.map(entry => [entry.path, entry.status])).toEqual([
      ['added.txt', 'added'],
      ['deleted.txt', 'deleted'],
      ['modified.txt', 'modified'],
      ['unchanged.txt', 'unchanged'],
    ])
    expect(snapshot.summary.counts).toEqual({
      added: 1,
      deleted: 1,
      modified: 1,
      unchanged: 1,
    })
  })

  test('compares symbolic links by their stored target without following them', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const baselineLinkTarget = path.join('..', 'outside-one')
    const targetLinkTarget = path.join('..', 'outside-two')
    await Promise.all([
      symlink(baselineLinkTarget, path.join(baseline, 'changed-link')),
      symlink(targetLinkTarget, path.join(target, 'changed-link')),
      symlink('missing-target', path.join(baseline, 'same-link')),
      symlink('missing-target', path.join(target, 'same-link')),
      symlink('loop-b', path.join(baseline, 'loop-a')),
      symlink('loop-a', path.join(baseline, 'loop-b')),
      symlink('loop-b', path.join(target, 'loop-a')),
      symlink('loop-a', path.join(target, 'loop-b')),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({ includeUnchanged: true }).entries.map(entry => [entry.path, 'baseline' in entry ? entry.baseline : undefined, 'target' in entry ? entry.target : undefined, entry.status])).toEqual([
      ['changed-link', { kind: 'symlink', linkTarget: baselineLinkTarget }, { kind: 'symlink', linkTarget: targetLinkTarget }, 'modified'],
      ['loop-a', { kind: 'symlink', linkTarget: 'loop-b' }, { kind: 'symlink', linkTarget: 'loop-b' }, 'unchanged'],
      ['loop-b', { kind: 'symlink', linkTarget: 'loop-a' }, { kind: 'symlink', linkTarget: 'loop-a' }, 'unchanged'],
      ['same-link', { kind: 'symlink', linkTarget: 'missing-target' }, { kind: 'symlink', linkTarget: 'missing-target' }, 'unchanged'],
    ])
  })

  test('reports entry kind and case-only path changes without rename detection', async () => {
    if (process.platform === 'win32')
      return
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'Readme.md'), 'same bytes'),
      writeFile(path.join(target, 'README.md'), 'same bytes'),
      writeFile(path.join(baseline, 'kind-change'), 'regular file'),
      symlink('missing-target', path.join(target, 'kind-change')),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({ includeUnchanged: true }).entries.map(entry => [entry.path, 'baseline' in entry ? entry.baseline : undefined, 'target' in entry ? entry.target : undefined, entry.status])).toEqual([
      ['README.md', undefined, { kind: 'file', size: 10 }, 'added'],
      ['Readme.md', { kind: 'file', size: 10 }, undefined, 'deleted'],
      ['kind-change', { kind: 'file', size: 12 }, { kind: 'symlink', linkTarget: 'missing-target' }, 'modified'],
    ])
  })

  test('records Entry State independently for each side of a kind change', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'kind-change'), 'regular file'),
      symlink('missing-target', path.join(target, 'kind-change')),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({}).entries).toEqual([{
      id: 1,
      path: 'kind-change',
      status: 'modified',
      baseline: { kind: 'file', size: 12 },
      target: { kind: 'symlink', linkTarget: 'missing-target' },
    }])
  })

  test('matches a kind filter against either Entry State', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'kind-change'), 'regular file'),
      symlink('missing-target', path.join(target, 'kind-change')),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result

    expect([
      snapshot.list({ kinds: ['file'] }).entries.map(entry => entry.path),
      snapshot.list({ kinds: ['symlink'] }).entries.map(entry => entry.path),
    ]).toEqual([['kind-change'], ['kind-change']])
  })

  test('applies hierarchical Target Directory gitignore rules to both trees', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      mkdir(path.join(baseline, 'nested')),
      mkdir(path.join(baseline, 'baseline-only')),
      mkdir(path.join(target, 'nested')),
    ])
    await Promise.all([
      writeFile(path.join(baseline, '.gitignore'), '*.tmp\nbaseline-only/\n'),
      writeFile(path.join(target, '.gitignore'), '*.tmp\nbaseline-only/\n'),
      writeFile(path.join(baseline, 'nested', '.gitignore'), '!keep.tmp\n'),
      writeFile(path.join(target, 'nested', '.gitignore'), '!keep.tmp\n'),
      writeFile(path.join(baseline, 'nested', 'drop.tmp'), 'ignored baseline'),
      writeFile(path.join(target, 'nested', 'drop.tmp'), 'ignored target'),
      writeFile(path.join(baseline, 'nested', 'keep.tmp'), 'before'),
      writeFile(path.join(target, 'nested', 'keep.tmp'), 'after'),
      writeFile(path.join(baseline, 'baseline-only', 'secret.txt'), 'ignored even though absent in target'),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result
    const entries = snapshot.list({ includeUnchanged: true }).entries

    expect(entries.map(entry => entry.path)).toEqual([
      '.gitignore',
      'nested/.gitignore',
      'nested/keep.tmp',
    ])
    expect(entries.find(entry => entry.path === 'nested/keep.tmp')?.status).toBe('modified')
  })

  test('always excludes repository metadata and native system junk at every depth', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const directories = [
      '.git',
      '.Spotlight-V100',
      '.Trashes',
      '$recycle.bin',
      'System Volume Information',
      'nested/.git',
    ]
    const files = [
      '.DS_Store',
      '._resource',
      'THUMBS.DB',
      'ehthumbs.db',
      'desktop.INI',
    ]

    for (const root of [baseline, target]) {
      await mkdir(path.join(root, 'nested'))
      for (const directory of directories) {
        await mkdir(path.join(root, directory), { recursive: true })
        await writeFile(path.join(root, directory, 'ignored.txt'), root)
      }
      for (const file of files)
        await writeFile(path.join(root, 'nested', file), root)
      await writeFile(path.join(root, 'visible.txt'), 'visible')
    }

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
      gitignore: false,
    })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({ includeUnchanged: true }).entries.map(entry => entry.path)).toEqual(['visible.txt'])
  })

  test('disables gitignore independently from repeatable explicit exclusions', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    for (const root of [baseline, target]) {
      await mkdir(path.join(root, 'nested'))
      await writeFile(path.join(root, '.gitignore'), 'ignored-by-rule.txt\n')
      await writeFile(path.join(root, 'ignored-by-rule.txt'), root)
      await writeFile(path.join(root, 'nested', 'excluded.log'), root)
      await writeFile(path.join(root, 'nested', 'excluded.tmp'), root)
      await writeFile(path.join(root, 'visible.txt'), 'same')
    }

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
      gitignore: false,
      exclusions: ['**/*.log', '**/*.tmp'],
    })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({ includeUnchanged: true }).entries.map(entry => entry.path)).toEqual([
      '.gitignore',
      'ignored-by-rule.txt',
      'visible.txt',
    ])
  })

  test('paginates the deterministically sorted entry list with opaque cursors', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all(['charlie.txt', 'alpha.txt', 'bravo.txt'].flatMap(name => [
      writeFile(path.join(baseline, name), name),
      writeFile(path.join(target, name), name),
    ]))

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result
    const firstPage = snapshot.list({ includeUnchanged: true, limit: 2 })
    const secondPage = snapshot.list({ includeUnchanged: true, limit: 2, cursor: firstPage.nextCursor })
    const anchorPage = snapshot.list({ includeUnchanged: true, limit: 2, anchor: 3 })

    expect(firstPage.entries.map(entry => entry.path)).toEqual(['alpha.txt', 'bravo.txt'])
    expect(firstPage.nextCursor).toBeString()
    expect(secondPage).toEqual({
      entries: [expect.objectContaining({ path: 'charlie.txt' })],
    })
    expect(anchorPage).toEqual({
      entries: [expect.objectContaining({ path: 'charlie.txt' })],
    })
    expect(() => snapshot.list({ includeUnchanged: true, anchor: 999 })).toThrow('anchor does not match')
  })

  test('filters entries by status, path text, and entry kind before pagination', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      mkdir(path.join(baseline, 'nested')),
      mkdir(path.join(target, 'nested')),
    ])
    await Promise.all([
      writeFile(path.join(baseline, 'nested', 'match.txt'), 'before'),
      writeFile(path.join(target, 'nested', 'match.txt'), 'after'),
      writeFile(path.join(baseline, 'outside.txt'), 'before'),
      writeFile(path.join(target, 'outside.txt'), 'after'),
      symlink('before', path.join(baseline, 'nested', 'link')),
      symlink('after-', path.join(target, 'nested', 'link')),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result

    expect(snapshot.list({
      includeUnchanged: true,
      statuses: ['modified'],
      path: 'NESTED',
      kinds: ['file'],
    }).entries.map(entry => entry.path)).toEqual(['nested/match.txt'])
  })

  test('returns lazy tree children with descendant status aggregates', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      mkdir(path.join(baseline, 'src', 'lib'), { recursive: true }),
      mkdir(path.join(target, 'src', 'lib'), { recursive: true }),
    ])
    await Promise.all([
      writeFile(path.join(target, 'src', 'added.ts'), 'added'),
      writeFile(path.join(baseline, 'src', 'lib', 'changed.ts'), 'before'),
      writeFile(path.join(target, 'src', 'lib', 'changed.ts'), 'after'),
      writeFile(path.join(baseline, 'README.md'), 'same'),
      writeFile(path.join(target, 'README.md'), 'same'),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result

    expect(snapshot.tree({ path: '' }).children).toEqual([
      {
        kind: 'directory',
        name: 'src',
        path: 'src',
        counts: { added: 1, deleted: 0, modified: 1, unchanged: 0 },
        issues: 0,
      },
      expect.objectContaining({
        kind: 'file',
        name: 'README.md',
        path: 'README.md',
        status: 'unchanged',
      }),
    ])
    expect(snapshot.tree({ path: 'src' }).children.map(child => child.path)).toEqual([
      'src/lib',
      'src/added.ts',
    ])
    expect(snapshot.search('SRC', ['modified']).results.map(child => child.path)).toEqual([
      'src',
      'src/lib',
      'src/lib/changed.ts',
    ])
    expect(snapshot.search('src', ['added'], 1)).toEqual({
      results: [expect.objectContaining({ kind: 'directory', path: 'src' })],
      truncated: true,
    })
  })

  test('classifies entry presentation lazily without treating invalid UTF-8 as text', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'code.ts'), 'const value = 1\n'),
      writeFile(path.join(target, 'code.ts'), 'const value = 2\n'),
      writeFile(path.join(baseline, 'logo.svg'), '<svg></svg>'),
      writeFile(path.join(target, 'logo.svg'), '<svg><path /></svg>'),
      writeFile(path.join(baseline, 'data.bin'), Uint8Array.from([0xC3, 0x28])),
      writeFile(path.join(target, 'data.bin'), Uint8Array.from([0xC3, 0x29])),
      symlink('missing-a', path.join(baseline, 'reference')),
      symlink('missing-b', path.join(target, 'reference')),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result
    const entries = snapshot.list({ includeUnchanged: true }).entries
    const classifications = await Promise.all(entries.map(async entry => [
      entry.path,
      (await snapshot.detail(entry.id)).presentation,
    ]))

    expect(classifications).toEqual([
      ['code.ts', 'text'],
      ['data.bin', 'binary'],
      ['logo.svg', 'image'],
      ['reference', 'symlink'],
    ])
  })

  test('decodes supported text encodings without changing byte-exact status', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const utf16le = Uint8Array.from([
      0xFF,
      0xFE,
      0x68,
      0x00,
      0x65,
      0x00,
      0x6C,
      0x00,
      0x6C,
      0x00,
      0x6F,
      0x00,
      0x0A,
      0x00,
    ])
    await Promise.all([
      writeFile(path.join(baseline, 'message.txt'), 'hello\n'),
      writeFile(path.join(target, 'message.txt'), utf16le),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({ includeUnchanged: true }).entries[0]!

    expect(entry.status).toBe('modified')
    expect(await snapshot.content(entry.id, 'baseline')).toEqual({
      status: 'ready',
      text: 'hello\n',
      encoding: 'utf-8',
      size: 6,
      lineCount: 2,
    })
    expect(await snapshot.content(entry.id, 'target')).toEqual({
      status: 'ready',
      text: 'hello\n',
      encoding: 'utf-16le',
      size: 14,
      lineCount: 2,
    })

    const utf16be = Uint8Array.from([
      0xFE,
      0xFF,
      0x00,
      0x68,
      0x00,
      0x69,
      0x00,
      0x0A,
    ])
    await writeFile(path.join(target, 'big-endian.txt'), utf16be)
    const refreshed = await workspace.refresh().result
    const bigEndian = refreshed.list({ includeUnchanged: true }).entries.find(item => item.path === 'big-endian.txt')!
    expect(await refreshed.content(bigEndian.id, 'target')).toEqual({
      status: 'ready',
      text: 'hi\n',
      encoding: 'utf-16be',
      size: 8,
      lineCount: 2,
    })
  })

  test('returns a bounded server-generated Text Difference for a modified file', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'changed.txt'), 'alpha\nbefore\nomega\n'),
      writeFile(path.join(target, 'changed.txt'), 'alpha\nafter\nomega\n'),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(await snapshot.textDiff(entry.id, { contextLines: 1 })).toEqual({
      status: 'ready',
      path: 'changed.txt',
      comparisonStatus: 'modified',
      contextLines: 1,
      baselineEncoding: 'utf-8',
      targetEncoding: 'utf-8',
      addedLines: 1,
      deletedLines: 1,
      patch: '--- baseline\n+++ target\n@@ -1,3 +1,3 @@\n alpha\n-before\n+after\n omega\n',
    })
  })

  test('rejects Text Difference context outside the supported range', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'changed.txt'), 'before\n'),
      writeFile(path.join(target, 'changed.txt'), 'after\n'),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    await expect(snapshot.textDiff(entry.id, { contextLines: 21 })).rejects.toThrow('contextLines must be an integer between 0 and 20')
  })

  test('does not provide a Text Difference for an Unchanged Entry', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'same.txt'), 'same\n'),
      writeFile(path.join(target, 'same.txt'), 'same\n'),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({ includeUnchanged: true }).entries[0]!

    await expect(snapshot.textDiff(entry.id)).rejects.toThrow('Comparison Entry not found')
  })

  test('uses /dev/null for Added and Deleted Text Differences', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'deleted.txt'), 'old\n'),
      writeFile(path.join(target, 'added.txt'), 'new\n'),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entries = snapshot.list({}).entries
    const results = await Promise.all(entries.map(entry => snapshot.textDiff(entry.id)))

    expect(results.map(result => [result.path, result.comparisonStatus, 'patch' in result ? result.patch : undefined])).toEqual([
      ['added.txt', 'added', '--- /dev/null\n+++ target\n@@ -0,0 +1,1 @@\n+new\n'],
      ['deleted.txt', 'deleted', '--- baseline\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-old\n'],
    ])
  })

  test('distinguishes encoding-only changes from Text Differences', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const utf16le = Uint8Array.from([
      0xFF,
      0xFE,
      0x68,
      0x00,
      0x65,
      0x00,
      0x6C,
      0x00,
      0x6C,
      0x00,
      0x6F,
      0x00,
      0x0A,
      0x00,
    ])
    await Promise.all([
      writeFile(path.join(baseline, 'message.txt'), 'hello\n'),
      writeFile(path.join(target, 'message.txt'), utf16le),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(await snapshot.textDiff(entry.id)).toEqual({
      status: 'no_textual_changes',
      path: 'message.txt',
      comparisonStatus: 'modified',
      reason: 'encoding_or_bom_only',
      baselineEncoding: 'utf-8',
      targetEncoding: 'utf-16le',
    })
  })

  test('reports binary content as an unavailable Text Difference', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'data.bin'), Uint8Array.from([0xC3, 0x28])),
      writeFile(path.join(target, 'data.bin'), Uint8Array.from([0xC3, 0x29])),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(await snapshot.textDiff(entry.id)).toEqual({
      status: 'unavailable',
      path: 'data.bin',
      comparisonStatus: 'modified',
      reason: 'non_text',
    })
  })

  test('reports a kind change as an unavailable Text Difference', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'reference'), 'text\n'),
      symlink('missing-target', path.join(target, 'reference')),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(await snapshot.textDiff(entry.id)).toEqual({
      status: 'unavailable',
      path: 'reference',
      comparisonStatus: 'modified',
      reason: 'mixed_entry_kinds',
    })
  })

  test('does not force Guarded content into a Text Difference', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await writeFile(path.join(target, 'guarded.txt'), 'x\n'.repeat(50_000))

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(await snapshot.textDiff(entry.id)).toEqual({
      status: 'unavailable',
      path: 'guarded.txt',
      comparisonStatus: 'added',
      reason: 'source_too_large',
      targetSize: 100_000,
      targetLineCount: 50_001,
    })
  })

  test('rejects a complete Text Difference above the output budget', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const largeChange = `${'x'.repeat(300 * 1024)}\n`
    await writeFile(path.join(target, 'large-change.txt'), largeChange)

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!
    const result = await snapshot.textDiff(entry.id)

    expect(result).toEqual({
      status: 'unavailable',
      path: 'large-change.txt',
      comparisonStatus: 'added',
      reason: 'output_too_large',
      addedLines: 1,
      deletedLines: 0,
      outputBytes: expect.any(Number),
    })
    expect('outputBytes' in result ? result.outputBytes : 0).toBeGreaterThan(256 * 1024)
  })

  test('stops a Text Difference that exceeds the complexity budget', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await writeFile(path.join(target, 'complex-change.txt'), '0123456789abcdef\n'.repeat(6_000))

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(await snapshot.textDiff(entry.id)).toEqual({
      status: 'unavailable',
      path: 'complex-change.txt',
      comparisonStatus: 'added',
      reason: 'complexity_limit',
    })
  }, 8_000)

  test('rejects Text Difference work above the concurrency limit', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const content = '0123456789abcdef\n'.repeat(1_000)
    await Promise.all([
      writeFile(path.join(target, 'first.txt'), content),
      writeFile(path.join(target, 'second.txt'), content),
      writeFile(path.join(target, 'third.txt'), content),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entries = snapshot.list({}).entries
    const first = snapshot.textDiff(entries[0]!.id)
    const second = snapshot.textDiff(entries[1]!.id)
    const third = await snapshot.textDiff(entries[2]!.id)

    expect(third).toEqual({
      status: 'unavailable',
      path: 'third.txt',
      comparisonStatus: 'added',
      reason: 'server_busy',
    })
    await Promise.all([first, second])
  }, 8_000)

  test('returns stale instead of mixing changed filesystem content into a snapshot', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'changing.txt'), 'before'),
      writeFile(path.join(target, 'changing.txt'), 'after!'),
    ])

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({ includeUnchanged: true }).entries[0]!
    const outside = path.join(path.dirname(target), 'outside-secret.txt')
    await writeFile(outside, 'secret bytes that must not be returned')
    await rm(path.join(target, 'changing.txt'))
    await symlink(outside, path.join(target, 'changing.txt'))

    expect(await snapshot.content(entry.id, 'target')).toEqual({ status: 'stale' })
    expect(await snapshot.blob(entry.id, 'target')).toEqual({ status: 'stale' })
    expect(await snapshot.textDiff(entry.id)).toEqual({
      status: 'unavailable',
      path: 'changing.txt',
      comparisonStatus: 'modified',
      reason: 'stale',
    })
  })

  test('publishes unreadable regular files as Comparison Issues', async () => {
    if (process.platform === 'win32' || process.getuid?.() === 0)
      return
    const { baseline, target } = await makeComparisonDirectories()
    const baselinePath = path.join(baseline, 'private.txt')
    const targetPath = path.join(target, 'private.txt')
    await Promise.all([
      writeFile(baselinePath, 'same size'),
      writeFile(targetPath, 'different'),
    ])
    await chmod(targetPath, 0)

    try {
      const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
      const snapshot = await workspace.refresh().result
      expect(snapshot.list({ includeUnchanged: true }).entries).toEqual([
        expect.objectContaining({
          path: 'private.txt',
          status: 'issue',
          kind: 'issue',
          message: expect.stringContaining('Comparison could not be completed'),
        }),
      ])
    }
    finally {
      await chmod(targetPath, 0o600)
    }
  })

  test('retries a file that changes after discovery before publishing its status', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const targetPath = path.join(target, 'changing.txt')
    await Promise.all([
      writeFile(path.join(baseline, 'changing.txt'), 'before'),
      writeFile(targetPath, 'before'),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    let changed = false
    const unsubscribe = workspace.observe((state) => {
      if (!changed && state.phase === 'comparing') {
        changed = true
        writeFileSync(targetPath, 'target')
      }
    })

    const snapshot = await workspace.refresh().result
    unsubscribe()
    expect(changed).toBe(true)
    expect(snapshot.list({ includeUnchanged: true }).entries).toEqual([
      expect.objectContaining({
        path: 'changing.txt',
        status: 'modified',
        baseline: { kind: 'file', size: 6 },
        target: { kind: 'file', size: 6 },
      }),
    ])
  })

  test('publishes a file that disappears after discovery as a Comparison Issue', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const targetPath = path.join(target, 'disappearing.txt')
    await Promise.all([
      writeFile(path.join(baseline, 'disappearing.txt'), 'same'),
      writeFile(targetPath, 'same'),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    let removed = false
    const unsubscribe = workspace.observe((state) => {
      if (!removed && state.phase === 'comparing') {
        removed = true
        rmSync(targetPath)
      }
    })

    const snapshot = await workspace.refresh().result
    unsubscribe()
    expect(removed).toBe(true)
    expect(snapshot.list({ includeUnchanged: true }).entries).toEqual([
      expect.objectContaining({ path: 'disappearing.txt', status: 'issue', kind: 'issue' }),
    ])
  })

  test('does not publish one-sided metadata that changes after discovery', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const targetPath = path.join(target, 'changing-added.txt')
    await writeFile(targetPath, 'before')
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    let changed = false
    const unsubscribe = workspace.observe((state) => {
      if (!changed && state.phase === 'comparing') {
        changed = true
        writeFileSync(targetPath, 'after!')
      }
    })

    await expect(workspace.refresh().result).rejects.toThrow(
      'Comparison Entry changed before snapshot publication',
    )
    unsubscribe()
    expect(workspace.snapshot()).toBeUndefined()
  })

  test('loads byte-exact image and binary blobs through opaque entry IDs', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const imageBytes = Uint8Array.from([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A])
    await writeFile(path.join(target, 'preview.png'), imageBytes)

    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({ includeUnchanged: true }).entries[0]!
    const blob = await snapshot.blob(entry.id, 'target')

    expect(blob.status).toBe('ready')
    if (blob.status === 'ready') {
      expect(blob.mimeType).toBe('image/png')
      expect(blob.filename).toBe('preview.png')
      expect(blob.bytes).toEqual(imageBytes)
    }
    expect(await snapshot.blob(entry.id, 'baseline')).toEqual({ status: 'missing' })
  })

  test('keeps the published snapshot intact when a refresh is canceled', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'value.txt'), 'before'),
      writeFile(path.join(target, 'value.txt'), 'after'),
    ])
    const workspace = await createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: target,
    })
    const originalSnapshot = await workspace.refresh().result

    for (let index = 0; index < 200; index++)
      await writeFile(path.join(target, `new-${index}.txt`), String(index))
    const refresh = workspace.refresh()
    refresh.cancel()
    await expect(refresh.result).rejects.toThrow()

    expect(workspace.snapshot()?.summary.id).toBe(originalSnapshot.summary.id)
    expect(workspace.state()).toEqual({
      phase: 'canceled',
      snapshotId: originalSnapshot.summary.id,
    })
  })

  test('publishes unsupported filesystem entries as visible Comparison Issues', async () => {
    if (process.platform === 'win32')
      return
    const { baseline, target } = await makeComparisonDirectories()
    const issuePath = path.join(target, 'service.pipe')
    const mkfifo = Bun.spawn(['mkfifo', issuePath], { stdout: 'ignore', stderr: 'pipe' })
    expect(await mkfifo.exited).toBe(0)

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const issue = snapshot.list({}).entries[0]!

    expect(snapshot.summary).toEqual(expect.objectContaining({
      counts: { added: 0, deleted: 0, modified: 0, unchanged: 0 },
      issues: 1,
    }))
    expect(issue).toEqual(expect.objectContaining({
      path: 'service.pipe',
      status: 'issue',
      kind: 'issue',
      message: expect.stringContaining('unsupported filesystem kind'),
    }))
    expect(await snapshot.detail(issue.id)).toEqual(expect.objectContaining({
      presentation: 'issue',
    }))
    expect(snapshot.tree({ path: '' }).children).toEqual([
      expect.objectContaining({ path: 'service.pipe', status: 'issue', kind: 'issue' }),
    ])
  })

  test('reports quantitative progress through every refresh phase', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(baseline, 'same.txt'), 'same'),
      writeFile(path.join(target, 'same.txt'), 'same'),
      writeFile(path.join(target, 'added.txt'), 'added'),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const states: ReturnType<typeof workspace.state>[] = []
    const unsubscribe = workspace.observe(state => states.push(state))
    await workspace.refresh().result
    unsubscribe()

    expect(states).toContainEqual(expect.objectContaining({
      phase: 'discovering',
      progress: expect.objectContaining({ discoveredEntries: 0 }),
    }))
    expect(states).toContainEqual(expect.objectContaining({
      phase: 'comparing',
      progress: expect.objectContaining({ totalEntries: 2, totalBytes: 4 }),
    }))
    expect(states).toContainEqual(expect.objectContaining({
      phase: 'publishing',
      progress: expect.objectContaining({ comparedEntries: 2, totalEntries: 2, comparedBytes: 4 }),
    }))
    expect(states.at(-1)).toEqual(expect.objectContaining({ phase: 'ready' }))
  })

  test('validates both comparison roots before a refresh can start', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const file = path.join(target, 'not-a-directory.txt')
    await writeFile(file, 'file')

    await expect(createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: file,
    })).rejects.toThrow('Target Directory must be a directory')
    await expect(createComparisonWorkspace({
      baselineDirectory: baseline,
      targetDirectory: baseline,
    })).rejects.toThrow('must be different')
  })

  test('rejects a Refresh after a fixed comparison root is replaced by a symlink', async () => {
    if (process.platform === 'win32')
      return
    const { baseline, target } = await makeComparisonDirectories()
    const outside = path.join(path.dirname(target), 'outside')
    await mkdir(outside)
    await writeFile(path.join(outside, 'secret.txt'), 'must stay outside the Comparison Workspace')
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const original = await workspace.refresh().result

    await rm(target, { recursive: true })
    await symlink(outside, target)

    await expect(workspace.refresh().result).rejects.toThrow(
      'Target Directory changed after the Comparison Workspace was created',
    )
    expect(workspace.snapshot()?.summary.id).toBe(original.summary.id)
    expect(workspace.snapshot()?.list({ includeUnchanged: true }).entries).toEqual([])
  })

  test('keeps published snapshot data immutable to in-process callers', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await writeFile(path.join(target, 'added.txt'), 'content')
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entry = snapshot.list({}).entries[0]!

    expect(Reflect.set(entry, 'path', 'tampered.txt')).toBe(false)
    expect(entry.status === 'issue' ? undefined : Reflect.set(entry.target!, 'size', 999)).toBe(false)
    expect(Reflect.set(snapshot.summary.counts, 'added', 999)).toBe(false)
    expect(Reflect.set(snapshot, 'summary', {})).toBe(false)
    expect(snapshot.list({}).entries[0]).toEqual({
      id: 1,
      path: 'added.txt',
      status: 'added',
      target: { kind: 'file', size: 7 },
    })
    expect(snapshot.summary.counts.added).toBe(1)
  })

  test('uses bytes rather than timestamps to decide content equality', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    const baselineEqual = path.join(baseline, 'equal.txt')
    const targetEqual = path.join(target, 'equal.txt')
    const baselineDifferent = path.join(baseline, 'different.txt')
    const targetDifferent = path.join(target, 'different.txt')
    await Promise.all([
      writeFile(baselineEqual, 'same bytes'),
      writeFile(targetEqual, 'same bytes'),
      writeFile(baselineDifferent, 'same size A'),
      writeFile(targetDifferent, 'same size B'),
    ])
    const timestamp = new Date('2024-01-01T00:00:00.000Z')
    await Promise.all([
      utimes(targetEqual, new Date(), new Date('2025-01-01T00:00:00.000Z')),
      utimes(baselineDifferent, timestamp, timestamp),
      utimes(targetDifferent, timestamp, timestamp),
    ])

    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    expect(snapshot.list({ includeUnchanged: true }).entries.map(entry => [entry.path, entry.status])).toEqual([
      ['different.txt', 'modified'],
      ['equal.txt', 'unchanged'],
    ])
  })

  test('guards large text and permanently blocks content beyond confirmed limits', async () => {
    const { baseline, target } = await makeComparisonDirectories()
    await Promise.all([
      writeFile(path.join(target, 'guarded.txt'), 'x\n'.repeat(50_000)),
      writeFile(path.join(target, 'blocked.txt'), 'x\n'.repeat(200_000)),
      writeFile(path.join(target, 'single-line.txt'), 'x'.repeat(1024 * 1024 + 1)),
    ])
    const workspace = await createComparisonWorkspace({ baselineDirectory: baseline, targetDirectory: target })
    const snapshot = await workspace.refresh().result
    const entries = snapshot.list({ includeUnchanged: true }).entries
    const blocked = entries.find(entry => entry.path === 'blocked.txt')!
    const guarded = entries.find(entry => entry.path === 'guarded.txt')!
    const singleLine = entries.find(entry => entry.path === 'single-line.txt')!

    expect(await snapshot.content(guarded.id, 'target')).toEqual({
      status: 'guarded',
      size: 100_000,
      lineCount: 50_001,
    })
    expect((await snapshot.content(guarded.id, 'target', true)).status).toBe('ready')
    expect(await snapshot.content(blocked.id, 'target')).toEqual({
      status: 'blocked',
      size: 400_000,
      lineCount: 200_001,
    })
    expect((await snapshot.content(blocked.id, 'target', true)).status).toBe('blocked')
    expect(await snapshot.content(singleLine.id, 'target', true)).toEqual({
      status: 'blocked',
      size: 1024 * 1024 + 1,
      lineCount: 1,
    })
  })
})
