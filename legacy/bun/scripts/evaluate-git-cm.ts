import type { CommitMessageModel } from '../src/commands/git/cm/model'
import type { ChatCompletionTokenUsage } from '../src/config/client'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { createCommitMessageEngine } from '../src/commands/git/cm/engine'
import { createOpenAICompatibleCommitMessageModel } from '../src/commands/git/cm/model'
import { resolveCmProfile } from '../src/config/cm'

interface CorpusEntry {
  commit: string
  parent: string
  subject: string
  expectedType?: string
  expectedScope?: string
  referenceIntent?: string
}

interface EvaluationResult {
  message?: string
  modelOutput?: string
  usage?: ChatCompletionTokenUsage
  estimatedLocalPromptTokens?: number
  fileCount?: number
  evidence?: string
  latencyMs?: number
  requestCount: number
  error?: string
}

interface EvaluationArtifact {
  generatedAt: string
  repository: string
  mode: 'prepare' | 'evaluate'
  profile?: { name: string, model: string }
  samples: Array<CorpusEntry & { legacy?: EvaluationResult, semantic?: EvaluationResult }>
  notes: string[]
}

interface BlindReviewArtifact {
  generatedAt: string
  sourceArtifact: string
  samples: Array<{
    commit: string
    fileCount?: number
    largeDiff: boolean
    candidateA: string
    candidateB: string
    review?: { winner: 'A' | 'B' | 'tie', notes?: string }
  }>
  notes: string[]
}

interface Arguments {
  count: number
  output: string
  profile?: string
  prepare: boolean
  reviewInput?: string
}

async function runGit(args: string[], cwd: string, allowFailure = false): Promise<string> {
  const proc = Bun.spawn(['git', ...args], { cwd, stdout: 'pipe', stderr: 'pipe' })
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ])
  if (exitCode !== 0 && !allowFailure)
    throw new Error(stderr.trim() || `git ${args.join(' ')} failed`)
  return stdout
}

function parseArguments(args: string[]): Arguments {
  let count = 30
  let output = path.join('.tmp', 'git-cm-evaluation.json')
  let profile: string | undefined
  let prepare = false
  let reviewInput: string | undefined
  for (const argument of args) {
    if (argument === '--prepare')
      prepare = true
    else if (argument.startsWith('--count='))
      count = Number(argument.slice('--count='.length))
    else if (argument.startsWith('--output='))
      output = argument.slice('--output='.length)
    else if (argument.startsWith('--profile='))
      profile = argument.slice('--profile='.length)
    else if (argument.startsWith('--blind-review='))
      reviewInput = argument.slice('--blind-review='.length)
    else
      throw new Error(`Unknown option: ${argument}`)
  }
  if (!Number.isInteger(count) || count < 30 || count > 100)
    throw new Error('--count must be an integer from 30 to 100')
  return { count, output, profile, prepare, reviewInput }
}

function reviewCandidates(sample: EvaluationArtifact['samples'][number]): Pick<BlindReviewArtifact['samples'][number], 'candidateA' | 'candidateB'> {
  const semanticFirst = Number.parseInt(sample.commit.slice(-2), 16) % 2 === 0
  const legacy = sample.legacy?.message ?? '[invalid model output]'
  const semantic = sample.semantic?.message ?? '[invalid model output]'
  return semanticFirst
    ? { candidateA: semantic, candidateB: legacy }
    : { candidateA: legacy, candidateB: semantic }
}

async function writeBlindReviewArtifact(args: Arguments): Promise<void> {
  const source = await Bun.file(args.reviewInput!).json() as EvaluationArtifact
  const artifact: BlindReviewArtifact = {
    generatedAt: new Date().toISOString(),
    sourceArtifact: args.reviewInput!,
    samples: source.samples.map(sample => ({
      commit: sample.commit,
      fileCount: sample.semantic?.fileCount ?? sample.legacy?.fileCount,
      largeDiff: (sample.semantic?.fileCount ?? sample.legacy?.fileCount ?? 0) > 10,
      ...reviewCandidates(sample),
    })),
    notes: [
      'Candidate assignments are intentionally omitted. Review each changed commit independently before selecting A, B, or tie.',
      'For largeDiff samples, score which message better states the primary change without unsupported behavior.',
    ],
  }
  await mkdir(path.dirname(args.output), { recursive: true })
  await writeFile(args.output, `${JSON.stringify(artifact, null, 2)}\n`)
  console.log(`Wrote ${artifact.samples.length} blind review samples to ${args.output}`)
}

function parseReference(subject: string): Pick<CorpusEntry, 'expectedType' | 'expectedScope' | 'referenceIntent'> {
  const separator = subject.indexOf(': ')
  if (separator === -1)
    return { referenceIntent: subject }
  const prefix = subject.slice(0, separator)
  const scopeStart = prefix.indexOf('(')
  const expectedType = scopeStart === -1 ? prefix : prefix.slice(0, scopeStart)
  const expectedScope = scopeStart === -1 || !prefix.endsWith(')') ? undefined : prefix.slice(scopeStart + 1, -1)
  return {
    expectedType: expectedType || undefined,
    expectedScope: expectedScope || undefined,
    referenceIntent: subject.slice(separator + 2).trim(),
  }
}

async function collectCorpus(repoRoot: string, count: number): Promise<CorpusEntry[]> {
  const output = await runGit(['log', `--max-count=${count}`, '--format=%H%x00%P%x00%s%x00'], repoRoot)
  const fields = output.split('\0').filter(Boolean)
  const samples: CorpusEntry[] = []
  for (let index = 0; index + 2 < fields.length; index += 3) {
    const [rawCommit, rawParents, rawSubject] = fields.slice(index, index + 3)
    const commit = rawCommit?.trim()
    const parent = rawParents?.trim().split(' ')[0]
    const subject = rawSubject?.trim()
    if (!commit || !parent || !subject)
      continue
    samples.push({ commit, parent, subject, ...parseReference(subject) })
  }
  return samples
}

async function createLegacyResult(repoRoot: string, entry: CorpusEntry, model: CommitMessageModel): Promise<EvaluationResult> {
  const [names, diff] = await Promise.all([
    runGit(['diff', '--name-status', '--find-renames', entry.parent, entry.commit], repoRoot),
    runGit(['diff', '--no-ext-diff', '--find-renames', entry.parent, entry.commit], repoRoot),
  ])
  const evidence = [
    `Changed files:\n${names.trim()}`,
    'Diffs:',
    diff.slice(0, 24_000),
  ].join('\n\n')
  const startedAt = performance.now()
  try {
    const result = await model.generate({
      system: 'Return only a concise Angular commit message in type(scope): subject format.',
      evidence,
    })
    return {
      message: result.content,
      usage: result.usage,
      fileCount: names.split('\n').filter(Boolean).length,
      latencyMs: Math.round(performance.now() - startedAt),
      requestCount: 1,
    }
  }
  catch (error) {
    return { error: (error as Error).message, latencyMs: Math.round(performance.now() - startedAt), requestCount: 1 }
  }
}

async function createSemanticResult(
  repoRoot: string,
  entry: CorpusEntry,
  model: CommitMessageModel,
): Promise<EvaluationResult> {
  const worktree = await mkdtemp(path.join(tmpdir(), 'ycy-cm-evaluation-'))
  try {
    await runGit(['worktree', 'add', '--detach', worktree, entry.commit], repoRoot)
    await runGit(['reset', '--mixed', entry.parent], worktree)
    const fileCount = (await runGit(['diff', '--name-only'], worktree)).split('\n').filter(Boolean).length
    const modelInputs: Parameters<CommitMessageModel['generate']>[0][] = []
    let modelOutput: string | undefined
    let modelUsage: ChatCompletionTokenUsage | undefined
    const recordingModel: CommitMessageModel = {
      async generate(input) {
        modelInputs.push(input)
        const output = await model.generate(input)
        modelOutput = output.content
        modelUsage = output.usage
        return output
      },
    }
    const startedAt = performance.now()
    try {
      const result = await createCommitMessageEngine({ model: recordingModel }).generate({
        repoRoot: worktree,
        scope: 'all-uncommitted',
        language: 'en',
        includeBody: false,
      })
      return {
        message: result.message,
        modelOutput,
        usage: result.usage,
        estimatedLocalPromptTokens: result.evidence.estimatedLocalPromptTokens,
        fileCount,
        evidence: modelInputs[0]?.evidence,
        latencyMs: Math.round(performance.now() - startedAt),
        requestCount: 1,
      }
    }
    catch (error) {
      return {
        error: (error as Error).message,
        modelOutput,
        usage: modelUsage,
        fileCount,
        evidence: modelInputs[0]?.evidence,
        latencyMs: Math.round(performance.now() - startedAt),
        requestCount: 1,
      }
    }
  }
  finally {
    await runGit(['worktree', 'remove', '--force', worktree], repoRoot, true)
    await rm(worktree, { recursive: true, force: true })
  }
}

async function main(): Promise<void> {
  const args = parseArguments(process.argv.slice(2))
  if (args.reviewInput) {
    await writeBlindReviewArtifact(args)
    return
  }
  const repoRoot = (await runGit(['rev-parse', '--show-toplevel'], process.cwd())).trim()
  const samples = await collectCorpus(repoRoot, args.count)
  if (samples.length < 30)
    throw new Error(`Only ${samples.length} commits with parents are available; evaluation requires at least 30.`)

  const artifact: EvaluationArtifact = {
    generatedAt: new Date().toISOString(),
    repository: repoRoot,
    mode: args.prepare ? 'prepare' : 'evaluate',
    samples,
    notes: [
      'expectedType, expectedScope, and referenceIntent are seed labels parsed from existing subjects and require human review before accuracy claims.',
      'Provider usage is authoritative. estimatedLocalPromptTokens is a local serialized-prompt estimate only.',
      'Both legacy and semantic rows issue one provider request per sample when evaluation mode is used.',
    ],
  }
  if (!args.prepare) {
    const profile = await resolveCmProfile(args.profile)
    const model = createOpenAICompatibleCommitMessageModel(profile)
    artifact.profile = { name: profile.name, model: profile.model }
    artifact.samples = []
    for (const sample of samples) {
      console.log(`Evaluating ${sample.commit.slice(0, 12)} (${artifact.samples.length + 1}/${samples.length})`)
      const legacy = await createLegacyResult(repoRoot, sample, model)
      const semantic = await createSemanticResult(repoRoot, sample, model)
      artifact.samples.push({ ...sample, legacy, semantic })
    }
  }
  await mkdir(path.dirname(args.output), { recursive: true })
  await writeFile(args.output, `${JSON.stringify(artifact, null, 2)}\n`)
  console.log(`Wrote ${artifact.samples.length} git cm evaluation samples to ${args.output}`)
}

if (import.meta.main) {
  try {
    await main()
  }
  catch (error) {
    console.error((error as Error).message)
    process.exitCode = 1
  }
}
