import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import {
  assertGitSnapshotCurrent,
  captureGitSnapshot,
  inspectGitChanges,
  stageFiles,
} from './changes'
import { isCommitMessageError } from './types'

const temporaryDirectories: string[] = []

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function runGit(repoRoot: string, args: string[]): Promise<string> {
  const proc = Bun.spawn(['git', '-C', repoRoot, ...args], { stdout: 'pipe', stderr: 'pipe' })
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ])
  if (exitCode !== 0)
    throw new Error(stderr.trim() || `git ${args.join(' ')} failed`)
  return stdout
}

async function createRepository(): Promise<string> {
  const repoRoot = await mkdtemp(path.join(tmpdir(), 'ycy-cm-changes-'))
  temporaryDirectories.push(repoRoot)
  await runGit(repoRoot, ['init', '-q'])
  await runGit(repoRoot, ['config', 'user.name', 'CM Test'])
  await runGit(repoRoot, ['config', 'user.email', 'cm-test@example.test'])
  return repoRoot
}

async function writeChangedFile(repoRoot: string, filePath: string, contents: string): Promise<void> {
  const absolutePath = path.join(repoRoot, filePath)
  await mkdir(path.dirname(absolutePath), { recursive: true })
  await writeFile(absolutePath, contents)
}

async function commitAll(repoRoot: string, message = 'test commit'): Promise<void> {
  await runGit(repoRoot, ['add', '-A'])
  await runGit(repoRoot, ['commit', '-qm', message])
}

describe('Git snapshot capture', () => {
  test('keeps more than 100 files selectable and in the complete snapshot', async () => {
    const repoRoot = await createRepository()
    const filePaths = Array.from({ length: 101 }, (_, index) => `src/file-${index}.ts`)
    await Promise.all(filePaths.map((filePath, index) => writeChangedFile(repoRoot, filePath, `export const file${index} = ${index}\n`)))

    const inspection = await inspectGitChanges({ cwd: repoRoot })
    const snapshot = await captureGitSnapshot({ repoRoot, scope: 'all-uncommitted' })

    expect(inspection.files).toHaveLength(101)
    expect(inspection.files.map(file => file.path)).toContain('src/file-100.ts')
    expect(snapshot.files).toHaveLength(101)

    await stageFiles(repoRoot, inspection.files.map(file => file.path))
    expect((await runGit(repoRoot, ['diff', '--cached', '--name-only'])).trim().split('\n')).toHaveLength(101)
  })

  test('captures rename, delete, untracked, and binary changes without exposing binary hunks', async () => {
    const repoRoot = await createRepository()
    await writeChangedFile(repoRoot, 'src/renamed.ts', 'export const renamed = false\n')
    await writeChangedFile(repoRoot, 'src/deleted.ts', 'export const deleted = true\n')
    await commitAll(repoRoot)
    await runGit(repoRoot, ['mv', 'src/renamed.ts', 'src/new-name.ts'])
    await writeChangedFile(repoRoot, 'src/new-name.ts', 'export const renamed = true\n')
    await runGit(repoRoot, ['rm', '-q', 'src/deleted.ts'])
    await writeChangedFile(repoRoot, 'src/untracked.ts', 'export const untracked = true\n')
    await writeChangedFile(repoRoot, 'assets/logo.png', '\0binary')

    const snapshot = await captureGitSnapshot({ repoRoot, scope: 'all-uncommitted' })
    const renamed = snapshot.files.find(file => file.path === 'src/new-name.ts')
    const binary = snapshot.files.find(file => file.path === 'assets/logo.png')

    expect(snapshot.files.map(file => file.path)).toEqual(expect.arrayContaining([
      'src/new-name.ts',
      'src/deleted.ts',
      'src/untracked.ts',
      'assets/logo.png',
    ]))
    expect(renamed?.originalPath).toBe('src/renamed.ts')
    expect(renamed?.stats).toEqual({ additions: 1, deletions: 1 })
    expect(binary).toMatchObject({ role: 'binary', contentPolicy: 'metadata-only', hunks: [] })
  })

  test('captures package manifest states for structured dependency evidence', async () => {
    const repoRoot = await createRepository()
    await writeChangedFile(repoRoot, 'package.json', JSON.stringify({ dependencies: { zod: '4.0.0' } }))
    await commitAll(repoRoot)
    await writeChangedFile(repoRoot, 'package.json', JSON.stringify({ dependencies: { zod: '4.4.3', vitest: '3.0.0' } }))

    const unstaged = await captureGitSnapshot({ repoRoot, scope: 'all-uncommitted' })
    expect(unstaged.files[0]?.manifest).toEqual({
      before: JSON.stringify({ dependencies: { zod: '4.0.0' } }),
      after: JSON.stringify({ dependencies: { zod: '4.4.3', vitest: '3.0.0' } }),
    })

    await runGit(repoRoot, ['add', 'package.json'])
    const staged = await captureGitSnapshot({ repoRoot, scope: 'staged' })
    expect(staged.files[0]?.manifest).toEqual(unstaged.files[0]?.manifest)
  })

  test('keeps staged and worktree patches separate while preserving their complete scopes', async () => {
    const repoRoot = await createRepository()
    await writeChangedFile(repoRoot, 'src/mixed.ts', 'export const value = 0\n')
    await writeChangedFile(repoRoot, 'src/staged.ts', 'export const staged = 0\n')
    await writeChangedFile(repoRoot, 'src/unstaged.ts', 'export const unstaged = 0\n')
    await commitAll(repoRoot)
    await writeChangedFile(repoRoot, 'src/mixed.ts', 'export const value = 1\n')
    await writeChangedFile(repoRoot, 'src/staged.ts', 'export const staged = 1\n')
    await runGit(repoRoot, ['add', 'src/mixed.ts', 'src/staged.ts'])
    await writeChangedFile(repoRoot, 'src/mixed.ts', 'export const value = 2\n')
    await writeChangedFile(repoRoot, 'src/unstaged.ts', 'export const unstaged = 1\n')

    const all = await captureGitSnapshot({ repoRoot, scope: 'all-uncommitted' })
    const staged = await captureGitSnapshot({ repoRoot, scope: 'staged' })
    const mixed = all.files.find(file => file.path === 'src/mixed.ts')

    expect(all.files.map(file => file.path)).toEqual(expect.arrayContaining(['src/mixed.ts', 'src/staged.ts', 'src/unstaged.ts']))
    expect(staged.files.map(file => file.path)).toEqual(['src/mixed.ts', 'src/staged.ts'])
    expect(mixed?.hunks.map(hunk => hunk.source)).toEqual(['staged', 'worktree'])
    expect(all.snapshotId).not.toBe(staged.snapshotId)
  })

  test('uses a stable snapshot ID and rejects a changed staged scope', async () => {
    const repoRoot = await createRepository()
    await writeChangedFile(repoRoot, 'src/value.ts', 'export const value = 0\n')
    await commitAll(repoRoot)
    await writeChangedFile(repoRoot, 'src/value.ts', 'export const value = 1\n')
    await runGit(repoRoot, ['add', 'src/value.ts'])

    const first = await captureGitSnapshot({ repoRoot, scope: 'staged' })
    const second = await captureGitSnapshot({ repoRoot, scope: 'staged' })
    expect(first.snapshotId).toBe(second.snapshotId)
    await assertGitSnapshotCurrent(repoRoot, 'staged', first.snapshotId)

    await writeChangedFile(repoRoot, 'src/value.ts', 'export const value = 2\n')
    await assertGitSnapshotCurrent(repoRoot, 'staged', first.snapshotId)
    await runGit(repoRoot, ['add', 'src/value.ts'])
    const error = await assertGitSnapshotCurrent(repoRoot, 'staged', first.snapshotId).catch(error => error)
    expect(isCommitMessageError(error, 'STALE_GIT_SCOPE')).toBe(true)
  })

  test('filters submodules from inspection and snapshots', async () => {
    const nestedRepo = await createRepository()
    await writeChangedFile(nestedRepo, 'nested.ts', 'export const nested = true\n')
    await commitAll(nestedRepo)
    const repoRoot = await createRepository()
    await runGit(repoRoot, ['-c', 'protocol.file.allow=always', 'submodule', 'add', '-q', nestedRepo, 'vendor/nested'])
    await commitAll(repoRoot)
    await writeChangedFile(repoRoot, 'vendor/nested/nested.ts', 'export const nested = false\n')
    await writeChangedFile(repoRoot, 'src/regular.ts', 'export const regular = true\n')

    const inspection = await inspectGitChanges({ cwd: repoRoot })
    const snapshot = await captureGitSnapshot({ repoRoot, scope: 'all-uncommitted' })

    expect(inspection.files.map(file => file.path)).toEqual(['src/regular.ts'])
    expect(snapshot.files.map(file => file.path)).toEqual(['src/regular.ts'])
  })
})
