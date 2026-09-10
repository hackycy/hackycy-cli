import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { assertGitSnapshotCurrent } from './changes'
import { createCommitMessageEngine } from './engine'
import { ScriptedCommitMessageModel } from './model'
import { isCommitMessageError } from './types'

const temporaryDirectories: string[] = []

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

async function runGit(repoRoot: string, args: string[]): Promise<void> {
  const proc = Bun.spawn(['git', '-C', repoRoot, ...args], { stdout: 'pipe', stderr: 'pipe' })
  const [stderr, exitCode] = await Promise.all([new Response(proc.stderr).text(), proc.exited])
  if (exitCode !== 0)
    throw new Error(stderr.trim() || `git ${args.join(' ')} failed`)
}

async function createRepository(): Promise<string> {
  const repoRoot = await mkdtemp(path.join(tmpdir(), 'ycy-cm-engine-'))
  temporaryDirectories.push(repoRoot)
  await runGit(repoRoot, ['init', '-q'])
  await runGit(repoRoot, ['config', 'user.name', 'CM Test'])
  await runGit(repoRoot, ['config', 'user.email', 'cm-test@example.test'])
  return repoRoot
}

async function writeFileAt(repoRoot: string, filePath: string, contents: string): Promise<void> {
  const absolutePath = path.join(repoRoot, filePath)
  await mkdir(path.dirname(absolutePath), { recursive: true })
  await writeFile(absolutePath, contents)
}

describe('CommitMessageEngine', () => {
  test('builds stable evidence and invokes the scripted model exactly once', async () => {
    const repoRoot = await createRepository()
    await writeFileAt(repoRoot, 'src/commands/cm/engine.ts', 'export function generateCommit(): void {}\n')
    const model = new ScriptedCommitMessageModel([{
      content: 'feat(cm): generate commit messages from evidence',
      usage: { promptTokens: 123, completionTokens: 9, totalTokens: 132 },
    }])
    const engine = createCommitMessageEngine({ model })

    const generated = await engine.generate({
      repoRoot,
      scope: 'all-uncommitted',
      language: 'en',
      includeBody: false,
    })

    expect(generated.message).toBe('feat(cm): generate commit messages from evidence')
    expect(generated.fileCount).toBe(1)
    expect(generated.usage?.promptTokens).toBe(123)
    expect(model.inputs).toHaveLength(1)
    expect(model.inputs[0]?.evidence).toContain('CHANGE_SUMMARY files=1')
    expect(model.inputs[0]?.evidence).toContain('DIRECTORY_CONTEXT')
    expect(model.inputs[0]?.system).toContain('English only; select evidence type: feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert')
    expect(model.inputs[0]?.system).toContain('feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert')
    expect(model.inputs[0]?.system).toContain('Infer scope from DIRECTORY_CONTEXT')
    expect(model.inputs[0]?.system).not.toContain('root file stem')
  })

  test('returns stable errors for no changes, model failures, and invalid output', async () => {
    const emptyRepo = await createRepository()
    const emptyModel = new ScriptedCommitMessageModel([{ content: 'feat(cm): unused' }])
    const noChanges = await createCommitMessageEngine({ model: emptyModel }).generate({
      repoRoot: emptyRepo,
      scope: 'all-uncommitted',
      language: 'en',
      includeBody: false,
    }).catch(error => error)
    expect(isCommitMessageError(noChanges, 'NO_CHANGES')).toBe(true)
    expect(emptyModel.inputs).toHaveLength(0)

    const repoRoot = await createRepository()
    await writeFileAt(repoRoot, 'src/value.ts', 'export const value = 1\n')
    const unavailable = new ScriptedCommitMessageModel([new Error('offline')])
    const modelError = await createCommitMessageEngine({ model: unavailable }).generate({
      repoRoot,
      scope: 'all-uncommitted',
      language: 'en',
      includeBody: false,
    }).catch(error => error)
    expect(isCommitMessageError(modelError, 'MODEL_UNAVAILABLE')).toBe(true)
    expect(unavailable.inputs).toHaveLength(1)

    const invalid = new ScriptedCommitMessageModel([{ content: 'Here is your commit message: feat(cm): invalid' }])
    const invalidOutput = await createCommitMessageEngine({ model: invalid }).generate({
      repoRoot,
      scope: 'all-uncommitted',
      language: 'en',
      includeBody: false,
    }).catch(error => error)
    expect(isCommitMessageError(invalidOutput, 'INVALID_MODEL_OUTPUT')).toBe(true)
    expect((invalidOutput as Error).message).toContain('Received model output: "Here is your commit message: feat(cm): invalid"')
    expect(invalid.inputs).toHaveLength(1)
  })

  test('accepts a nonempty scope containing spaces', async () => {
    const repoRoot = await createRepository()
    await writeFileAt(repoRoot, 'src/value.ts', 'export const value = 1\n')
    const model = new ScriptedCommitMessageModel([{ content: 'feat(git cm): add commit message generation' }])

    const generated = await createCommitMessageEngine({ model }).generate({
      repoRoot,
      scope: 'all-uncommitted',
      language: 'en',
      includeBody: false,
    })

    expect(generated.message).toBe('feat(git cm): add commit message generation')
  })

  test('redacts sensitive paths from model evidence and supports validated bodies', async () => {
    const repoRoot = await createRepository()
    await writeFileAt(repoRoot, '.env', 'API_KEY=do-not-send-this-secret\n')
    await writeFileAt(repoRoot, 'src/value.ts', 'export const value = 1\n')
    const model = new ScriptedCommitMessageModel([{
      content: 'feat(value): add value export\n\nKeep the value available to callers.',
    }])
    const generated = await createCommitMessageEngine({ model }).generate({
      repoRoot,
      scope: 'all-uncommitted',
      language: 'en',
      includeBody: true,
    })

    expect(generated.evidence.contentCompacted).toBe(false)
    expect(model.inputs[0]?.evidence).toContain('protected=0/1')
    expect(model.inputs[0]?.evidence).not.toContain('do-not-send-this-secret')
    await assertGitSnapshotCurrent(repoRoot, 'all-uncommitted', generated.snapshotId)
  })
})
