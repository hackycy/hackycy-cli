import type {
  RecommendationConfidence,
  SourceSelectionModel,
  WorkspaceInspection,
} from './discovery'
import path from 'node:path'
import { DEFAULT_GLOB_PATTERN, ZIP_GLOB_OPTIONS } from './archive'
import {
  buildSourceSelectionModel,
  candidateHint,
  confidenceLabel,
  describeProjectKind,
  inspectWorkspaceRoot,
  normalizeRelativePath,
  sanitizeFileName,
} from './discovery'

interface StepNote {
  title: string
  lines: string[]
}

interface ChoiceOption<T> {
  value: T
  label: string
  hint?: string
}

interface ZipPlan {
  input: string
  file: string
  glob: string[]
  packageRoot: string
  packageName?: string
  confidence: RecommendationConfidence
}

interface ZipPlanningSession {
  rootDir: string
  workspace: WorkspaceInspection
  packageRoot?: string
  packageName?: string
  sourceSelection?: SourceSelectionModel
  selectedSource?: string
  globPatterns?: string[]
  outputFileName?: string
}

interface SelectPackageStep {
  type: 'select-package'
  note?: StepNote
  message: string
  options: Array<ChoiceOption<string>>
}

interface SelectSourceStep {
  type: 'select-source'
  note?: StepNote
  message: string
  options: Array<ChoiceOption<string>>
}

interface SelectGlobStep {
  type: 'select-glob'
  message: string
  options: Array<ChoiceOption<string>>
  initialValues: string[]
}

interface EditOutputFileStep {
  type: 'edit-output-file'
  message: string
  initialValue: string
}

interface CompleteStep {
  type: 'complete'
  note: StepNote
  plan: ZipPlan
}

type ZipPlanningStep = SelectPackageStep | SelectSourceStep | SelectGlobStep | EditOutputFileStep | CompleteStep

type ZipPlanningAnswer
  = | { type: 'package-root', value: string }
    | { type: 'source-directory', value: string }
    | { type: 'glob-patterns', value: string[] }
    | { type: 'output-file', value: string }

function summarizeItems(values: string[], maxItems = 2): string {
  if (values.length <= maxItems)
    return values.join(', ')

  const visible = values.slice(0, maxItems).join(', ')
  return `${visible}, +${values.length - maxItems} more`
}

function defaultOutputName(session: ZipPlanningSession): string {
  const fallback = session.packageName ?? path.basename(session.packageRoot ?? session.rootDir)
  return sanitizeFileName(fallback)
}

function normalizeSelectedPatterns(selectedPatterns: string[]): string[] {
  if (!selectedPatterns.includes(DEFAULT_GLOB_PATTERN))
    return selectedPatterns.length > 0 ? [...selectedPatterns] : [DEFAULT_GLOB_PATTERN]

  return [DEFAULT_GLOB_PATTERN]
}

async function hydrateSourceSelection(session: ZipPlanningSession): Promise<ZipPlanningSession> {
  if (!session.packageRoot || session.sourceSelection)
    return session

  const sourceSelection = await buildSourceSelectionModel(session.packageRoot)
  return {
    ...session,
    packageName: sourceSelection.packageName ?? session.packageName,
    sourceSelection,
  }
}

function buildPackageStep(session: ZipPlanningSession): SelectPackageStep {
  return {
    type: 'select-package',
    note: {
      title: 'Monorepo detected',
      lines: [
        `Found ${session.workspace.packages.length} workspace package${session.workspace.packages.length === 1 ? '' : 's'}`,
        `Signals: ${summarizeItems(session.workspace.reasons)}`,
      ],
    },
    message: 'Select a workspace package to zip:',
    options: session.workspace.packages.map(pkg => ({
      value: pkg.root,
      label: normalizeRelativePath(session.rootDir, pkg.root),
      hint: pkg.packageName ? `package: ${pkg.packageName}` : 'workspace package',
    })),
  }
}

function buildSourceStep(session: ZipPlanningSession): SelectSourceStep {
  const sourceSelection = session.sourceSelection!
  const lowConfidence = sourceSelection.confidence === 'low'

  return {
    type: 'select-source',
    note: {
      title: 'Artifact selection',
      lines: lowConfidence
        ? [
            `Project type: ${describeProjectKind(sourceSelection.projectSignals.kind)}`,
            `Confidence: ${confidenceLabel(sourceSelection.confidence)}`,
            'No clear build output was found. Pick the directory to ship.',
          ]
        : [
            `Project type: ${describeProjectKind(sourceSelection.projectSignals.kind)}`,
            `Confidence: ${confidenceLabel(sourceSelection.confidence)}`,
            `Recommended: ${sourceSelection.recommended.relative}`,
          ],
    },
    message: 'Select a directory to zip:',
    options: sourceSelection.candidates.map((candidate, index) => ({
      value: candidate.absolute,
      label: index === 0 ? `${candidate.relative} (recommended)` : candidate.relative,
      hint: candidateHint(candidate, sourceSelection.confidence, index === 0),
    })),
  }
}

function buildCompleteStep(session: ZipPlanningSession): CompleteStep {
  const packageRoot = session.packageRoot!
  const plan: ZipPlan = {
    input: session.selectedSource!,
    file: session.outputFileName!,
    glob: session.globPatterns!,
    packageRoot,
    packageName: session.packageName,
    confidence: session.sourceSelection!.confidence,
  }

  return {
    type: 'complete',
    note: {
      title: 'Zip plan',
      lines: [
        `Package: ${session.packageName ?? path.basename(packageRoot)}`,
        `Source: ${normalizeRelativePath(packageRoot, plan.input)}`,
        `Patterns: ${plan.glob.join(', ')}`,
        `Output: ${plan.file}.zip`,
      ],
    },
    plan,
  }
}

export async function createZipPlanningSession(dir: string): Promise<ZipPlanningSession> {
  const rootDir = path.resolve(dir)
  const workspace = await inspectWorkspaceRoot(rootDir)
  const defaultPackage = workspace.defaultPackage

  return {
    rootDir,
    workspace,
    packageRoot: workspace.packages.length > 0 ? undefined : defaultPackage.root,
    packageName: workspace.packages.length > 0 ? undefined : defaultPackage.packageName,
  }
}

export function applyZipPlanningAnswer(session: ZipPlanningSession, answer: ZipPlanningAnswer): ZipPlanningSession {
  switch (answer.type) {
    case 'package-root': {
      const selectedPackage = session.workspace.packages.find(pkg => pkg.root === path.resolve(answer.value))
      return {
        ...session,
        packageRoot: path.resolve(answer.value),
        packageName: selectedPackage?.packageName,
        sourceSelection: undefined,
        selectedSource: undefined,
        globPatterns: undefined,
        outputFileName: undefined,
      }
    }

    case 'source-directory':
      return {
        ...session,
        selectedSource: path.resolve(answer.value),
      }

    case 'glob-patterns':
      return {
        ...session,
        globPatterns: normalizeSelectedPatterns(answer.value),
      }

    case 'output-file':
      return {
        ...session,
        outputFileName: sanitizeFileName(answer.value),
      }
  }
}

export async function resolveZipPlanningStep(session: ZipPlanningSession): Promise<{ session: ZipPlanningSession, step: ZipPlanningStep }> {
  if (!session.packageRoot && session.workspace.packages.length > 0) {
    return {
      session,
      step: buildPackageStep(session),
    }
  }

  const hydratedSession = await hydrateSourceSelection(session)

  if (!hydratedSession.selectedSource) {
    return {
      session: hydratedSession,
      step: buildSourceStep(hydratedSession),
    }
  }

  if (!hydratedSession.globPatterns) {
    return {
      session: hydratedSession,
      step: {
        type: 'select-glob',
        message: 'Select file patterns to include in the zip:',
        options: ZIP_GLOB_OPTIONS.map(option => ({ value: option.value, label: option.label })),
        initialValues: [DEFAULT_GLOB_PATTERN],
      },
    }
  }

  if (!hydratedSession.outputFileName) {
    return {
      session: hydratedSession,
      step: {
        type: 'edit-output-file',
        message: 'Enter the name for the zip file (without .zip extension):',
        initialValue: defaultOutputName(hydratedSession),
      },
    }
  }

  return {
    session: hydratedSession,
    step: buildCompleteStep(hydratedSession),
  }
}

export type {
  CompleteStep,
  EditOutputFileStep,
  SelectGlobStep,
  SelectPackageStep,
  SelectSourceStep,
  StepNote,
  ZipPlan,
  ZipPlanningAnswer,
  ZipPlanningSession,
  ZipPlanningStep,
}
