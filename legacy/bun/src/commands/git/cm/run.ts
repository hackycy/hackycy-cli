import type { ChatCompletionTokenUsage } from '../../../config/client'
import type { ResolvedCmProfile } from '../../../config/types'
import type { CmOptions, CommitLanguage, EvidenceCoverage, GeneratedCommitMessage, GitScope } from './types'
import process from 'node:process'
import * as p from '@clack/prompts'
import ansis from 'ansis'
import { resolveCmProfile } from '../../../config/cm'
import {
  assertGitSnapshotCurrent,
  commitWithMessage,
  getRepoRoot,
  inspectGitChanges,
  pushChanges,
  stageAllChanges,
  stageFiles,
  unstageFiles,
} from './changes'
import { createCommitMessageEngine } from './engine'
import { createOpenAICompatibleCommitMessageModel } from './model'
import { isCommitMessageError } from './types'

function normalizeLanguage(lang: string | undefined): CommitLanguage {
  if (!lang)
    return 'en'
  if (lang !== 'en' && lang !== 'zh')
    throw new Error('Unsupported language. Use "en" or "zh".')
  return lang
}

function formatTokenCount(value: number | undefined): string {
  return value === undefined ? 'unknown' : value.toLocaleString('en-US')
}

function formatTokenUsage(usage: ChatCompletionTokenUsage | undefined): string {
  if (!usage)
    return 'Provider tokens: unavailable'
  return [
    `Provider tokens: ${formatTokenCount(usage.promptTokens)} prompt`,
    `${formatTokenCount(usage.completionTokens)} completion`,
    `${formatTokenCount(usage.totalTokens)} total`,
  ].join(' / ')
}

function formatEvidenceCoverage(evidence: EvidenceCoverage): string {
  return `Local evidence estimate: ~${evidence.estimatedLocalPromptTokens.toLocaleString('en-US')} serialized prompt tokens / ${evidence.representedClusters} of ${evidence.totalClusters} clusters / ${evidence.includedFacts} of ${evidence.includedFacts + evidence.omittedFacts} facts`
}

export function formatGeneratedMessage(
  message: string,
  profile: ResolvedCmProfile,
  tokenUsage: ChatCompletionTokenUsage | undefined,
  evidence: EvidenceCoverage,
): string {
  const lines = [
    ansis.green(message),
    '',
    ansis.dim(`Profile: ${profile.name} (${profile.model})`),
    ansis.dim(formatTokenUsage(tokenUsage)),
    ansis.dim(formatEvidenceCoverage(evidence)),
  ]
  if (evidence.contentCompacted) {
    lines.push(ansis.yellow(
      `Commit scope: ${evidence.totalClusters} cluster${evidence.totalClusters === 1 ? '' : 's'} represented with compacted semantic evidence. This does not affect which files are committed.`,
    ))
  }
  return lines.join('\n')
}

function printGeneratedMessage(generated: GeneratedCommitMessage, profile: ResolvedCmProfile): void {
  p.note(formatGeneratedMessage(generated.message, profile, generated.usage, generated.evidence), 'Commit message', { format: line => line })
}

async function promptForStageFiles(repoRoot: string, files: Awaited<ReturnType<typeof inspectGitChanges>>['files']): Promise<void> {
  if (files.length === 0) {
    p.log.info('No uncommitted changes.')
    return
  }
  const selected = await p.multiselect({
    message: 'Select files to stage',
    options: files.map(file => ({ value: file.path, label: file.status })),
    initialValues: files.map(file => file.path),
  })
  if (p.isCancel(selected)) {
    p.cancel('Cancelled')
    process.exit(0)
  }
  const selectedPaths = selected as string[]
  if (selectedPaths.length === 0) {
    p.cancel('Nothing selected.')
    process.exit(0)
  }
  const selectedSet = new Set(selectedPaths)
  const unselectedPaths = files
    .filter(file => file.indexStatus !== '?')
    .map(file => file.path)
    .filter(filePath => !selectedSet.has(filePath))
  const stageSpin = p.spinner()
  stageSpin.start('Staging selected changes...')
  try {
    if (unselectedPaths.length > 0)
      await unstageFiles(repoRoot, unselectedPaths)
    await stageFiles(repoRoot, selectedPaths)
    stageSpin.clear()
  }
  catch (error) {
    stageSpin.clear()
    p.log.error((error as Error).message)
    process.exit(1)
  }
}

export async function runGitCm(options: CmOptions): Promise<void> {
  if ((options.stage || options.stagePush) && options.stageAll) {
    p.log.error('Use either --stage/--stage-push or --stage-all, not both.')
    process.exit(1)
  }
  if ((options.stage || options.stagePush) && options.dryRun) {
    p.log.error('Use either --stage/--stage-push or --dry-run, not both.')
    process.exit(1)
  }
  const pushOption = options.stagePush || options.push
  if (pushOption && options.dryRun) {
    p.log.error('Use either --push/--stage-push or --dry-run, not both.')
    process.exit(1)
  }
  if (options.push && !options.stage && !options.staged && !options.stageAll && !options.stagePush) {
    p.log.error('Use --push with --stage, --staged, or --stage-all.')
    process.exit(1)
  }

  const shouldPromptStage = Boolean(options.stage || options.stagePush)
  const shouldStageAll = Boolean(options.stageAll && !options.dryRun)
  const stagedOnly = Boolean(options.staged || shouldPromptStage || shouldStageAll)
  const shouldCreateCommit = stagedOnly && !options.dryRun
  const shouldPush = Boolean(pushOption && shouldCreateCommit)
  const pushRemote = typeof pushOption === 'string' ? pushOption : undefined
  const interactive = Boolean(options.stage || options.stagePush || options.staged || options.stageAll || options.push)
  let repoRoot: string | undefined

  if (shouldPromptStage) {
    const spin = p.spinner()
    spin.start('Collecting git changes...')
    try {
      const inspection = await inspectGitChanges()
      repoRoot = inspection.repoRoot
      spin.clear()
      await promptForStageFiles(repoRoot, inspection.files)
    }
    catch (error) {
      spin.clear()
      p.log.error((error as Error).message)
      process.exit(1)
    }
  }
  if (shouldStageAll) {
    const stageSpin = p.spinner()
    stageSpin.start('Staging all changes...')
    try {
      repoRoot = await getRepoRoot()
      await stageAllChanges(repoRoot)
      stageSpin.clear()
    }
    catch (error) {
      stageSpin.clear()
      p.log.error((error as Error).message)
      process.exit(1)
    }
  }
  repoRoot ??= await getRepoRoot()
  const scope: GitScope = stagedOnly ? 'staged' : 'all-uncommitted'
  try {
    const inspection = await inspectGitChanges({ repoRoot, scope })
    if (inspection.files.length === 0) {
      p.log.info(scope === 'staged' ? 'No staged changes.' : 'No uncommitted changes.')
      return
    }
  }
  catch (error) {
    p.log.error((error as Error).message)
    process.exit(1)
  }

  let profile: ResolvedCmProfile
  try {
    profile = await resolveCmProfile(options.profile, options.timeoutMs)
  }
  catch (error) {
    p.log.error((error as Error).message)
    process.exit(1)
  }

  const spin = interactive ? p.spinner() : undefined
  spin?.start(stagedOnly ? 'Generating from staged changes...' : 'Generating from git changes...')
  spin?.message(`Generating commit message with ${profile.name}...`)
  const engine = createCommitMessageEngine({ model: createOpenAICompatibleCommitMessageModel(profile) })
  let generated: GeneratedCommitMessage
  try {
    generated = await engine.generate({
      repoRoot,
      scope,
      language: normalizeLanguage(options.lang),
      includeBody: Boolean(options.body),
    })
  }
  catch (error) {
    spin?.clear()
    if (isCommitMessageError(error, 'NO_CHANGES')) {
      if (interactive)
        p.log.info(error.message)
      else
        p.log.info(error.message)
      return
    }
    p.log.error((error as Error).message)
    p.log.info(`Provider: ${profile.name}`)
    p.log.info(`Base URL: ${profile.baseURL}`)
    p.log.info(`Model: ${profile.model}`)
    process.exit(1)
  }
  spin?.clear()
  printGeneratedMessage(generated, profile)
  if (!shouldCreateCommit)
    return

  const confirmed = await p.confirm({ message: 'Create this commit?' })
  if (p.isCancel(confirmed) || !confirmed) {
    p.outro('Cancelled')
    return
  }
  const commitSpin = p.spinner()
  commitSpin.start('Creating commit...')
  try {
    await assertGitSnapshotCurrent(repoRoot, scope, generated.snapshotId)
    await commitWithMessage(repoRoot, generated.message)
    commitSpin.clear()
  }
  catch (error) {
    commitSpin.clear()
    p.log.error((error as Error).message)
    process.exit(1)
  }
  if (!shouldPush) {
    p.outro(ansis.green('Commit created'))
    return
  }
  const pushSpin = p.spinner()
  pushSpin.start('Pushing to remote...')
  try {
    await pushChanges(repoRoot, pushRemote)
    pushSpin.clear()
    p.outro(ansis.green('Commit created and pushed'))
  }
  catch (error) {
    pushSpin.clear()
    p.log.error((error as Error).message)
    process.exit(1)
  }
}
