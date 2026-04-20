import fs from 'node:fs/promises'
import path from 'node:path'

const VITE_CONFIG_FILES = [
  'vite.config.ts',
  'vite.config.js',
  'vite.config.mts',
  'vite.config.mjs',
  'vite.config.cts',
  'vite.config.cjs',
]

const WEBPACK_CONFIG_FILES = [
  'webpack.config.ts',
  'webpack.config.js',
  'webpack.config.mts',
  'webpack.config.mjs',
  'webpack.config.cts',
  'webpack.config.cjs',
]

const UNIAPP_SIGNAL_FILES = [
  'pages.json',
  'src/pages.json',
  'manifest.json',
  'src/manifest.json',
  'uni.config.ts',
  'uni.config.js',
]

const SCAN_IGNORED_DIRS = new Set([
  '.git',
  '.hg',
  '.idea',
  '.nx',
  '.svn',
  '.turbo',
  '.vscode',
  'coverage',
  'node_modules',
])

const KNOWN_DIR_NAME_SCORES: Record<string, number> = {
  build: 26,
  dist: 30,
  h5: 24,
  out: 24,
  public: 12,
  release: 20,
  unpackage: 18,
}

type ProjectKind = 'vite' | 'webpack' | 'uniapp-h5' | 'frontend' | 'generic'
type RecommendationConfidence = 'high' | 'medium' | 'low'

interface ProjectSignals {
  kind: ProjectKind
  reasons: string[]
  hasIndexHtml: boolean
}

interface PackageSelection {
  root: string
  packageName?: string
}

interface CandidateDirectory {
  absolute: string
  relative: string
  score: number
  reasons: string[]
}

interface ScannedDirectory {
  absolute: string
  relative: string
  depth: number
}

interface ArtifactSpec {
  appliesTo: Array<ProjectKind | 'all'>
  baseScore: number
  reason: string
  relative: string
}

interface WorkspaceInspection {
  root: string
  reasons: string[]
  packages: PackageSelection[]
  defaultPackage: PackageSelection
}

interface SourceSelectionModel {
  packageRoot: string
  packageName?: string
  projectSignals: ProjectSignals
  candidates: CandidateDirectory[]
  recommended: CandidateDirectory
  confidence: RecommendationConfidence
}

const ARTIFACT_DIRECTORY_SPECS: ArtifactSpec[] = [
  {
    relative: 'dist/build/h5',
    appliesTo: ['uniapp-h5'],
    baseScore: 100,
    reason: 'matches common uniapp h5 output',
  },
  {
    relative: 'unpackage/dist/build/h5',
    appliesTo: ['uniapp-h5'],
    baseScore: 98,
    reason: 'matches common uniapp h5 output',
  },
  {
    relative: 'dist/dev/h5',
    appliesTo: ['uniapp-h5'],
    baseScore: 76,
    reason: 'matches uniapp h5 dev output',
  },
  {
    relative: 'unpackage/dist/dev/h5',
    appliesTo: ['uniapp-h5'],
    baseScore: 74,
    reason: 'matches uniapp h5 dev output',
  },
  {
    relative: 'dist',
    appliesTo: ['vite', 'webpack', 'frontend', 'generic'],
    baseScore: 88,
    reason: 'matches a standard frontend output directory',
  },
  {
    relative: 'build',
    appliesTo: ['webpack', 'frontend', 'generic'],
    baseScore: 82,
    reason: 'matches a standard frontend output directory',
  },
  {
    relative: 'out',
    appliesTo: ['frontend', 'generic'],
    baseScore: 78,
    reason: 'matches a standard frontend output directory',
  },
  {
    relative: 'release',
    appliesTo: ['frontend', 'generic'],
    baseScore: 74,
    reason: 'matches a standard release directory',
  },
  {
    relative: 'public',
    appliesTo: ['frontend', 'generic'],
    baseScore: 42,
    reason: 'public is available, but may still be source assets',
  },
]

function toRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))]
}

function normalizeGlobPattern(pattern: string): string {
  return pattern.replace(/^\.\//, '').replace(/\\/g, '/').replace(/\/$/, '')
}

function mergeReasonLists(existing: string[], incoming: string[]): string[] {
  return uniqueStrings([...existing, ...incoming])
}

function addCandidate(candidateMap: Map<string, CandidateDirectory>, candidate: CandidateDirectory): void {
  const existing = candidateMap.get(candidate.absolute)
  if (!existing) {
    candidateMap.set(candidate.absolute, candidate)
    return
  }

  existing.score = Math.max(existing.score, candidate.score)
  existing.reasons = mergeReasonLists(existing.reasons, candidate.reasons)
}

export function normalizeRelativePath(root: string, target: string): string {
  return path.relative(root, target) || '.'
}

export function sanitizeFileName(value: string): string {
  const leaf = value.split('/').pop() ?? value
  return leaf
    .trim()
    // eslint-disable-next-line no-control-regex
    .replace(/[<>:"/\\|?*\u0000-\u001F]/g, '-')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^\.+/, '')
    .replace(/\.+$/, '') || 'archive'
}

export function describeProjectKind(kind: ProjectKind): string {
  switch (kind) {
    case 'vite':
      return 'Vite frontend'
    case 'webpack':
      return 'Webpack frontend'
    case 'uniapp-h5':
      return 'uniapp h5 frontend'
    case 'frontend':
      return 'generic frontend'
    default:
      return 'generic directory'
  }
}

export function confidenceLabel(confidence: RecommendationConfidence): string {
  switch (confidence) {
    case 'high':
      return 'high'
    case 'medium':
      return 'medium'
    default:
      return 'low'
  }
}

export function candidateHint(candidate: CandidateDirectory, confidence: RecommendationConfidence, recommended: boolean): string | undefined {
  if (recommended)
    return `${confidenceLabel(confidence)} confidence`

  if (candidate.relative === '.')
    return 'package root'

  if (candidate.reasons.includes('surface-level candidate for manual review') || candidate.reasons.includes('fallback candidate for manual selection'))
    return 'manual review'

  if (candidate.reasons.includes('contains index.html'))
    return 'contains index.html'

  return 'possible output'
}

function workspacePatternsFromPackageJson(pkg: Record<string, unknown> | null): string[] {
  if (!pkg)
    return []

  const rawWorkspaces = pkg.workspaces
  if (Array.isArray(rawWorkspaces)) {
    return rawWorkspaces.filter((value): value is string => typeof value === 'string' && value.trim() !== '')
  }

  const workspaceConfig = toRecord(rawWorkspaces)
  const packages = workspaceConfig?.packages
  if (!Array.isArray(packages))
    return []

  return packages.filter((value): value is string => typeof value === 'string' && value.trim() !== '')
}

async function pathExists(filePath: string): Promise<boolean> {
  try {
    await fs.access(filePath)
    return true
  }
  catch {
    return false
  }
}

async function isDirectory(filePath: string): Promise<boolean> {
  try {
    const stat = await fs.stat(filePath)
    return stat.isDirectory()
  }
  catch {
    return false
  }
}

async function readJsonFile(filePath: string): Promise<Record<string, unknown> | null> {
  const file = Bun.file(filePath)
  if (!(await file.exists()))
    return null

  try {
    return toRecord(await file.json())
  }
  catch {
    return null
  }
}

async function readPnpmWorkspacePatterns(root: string): Promise<string[]> {
  const workspaceFile = path.join(root, 'pnpm-workspace.yaml')
  if (!(await pathExists(workspaceFile)))
    return []

  try {
    const contents = await Bun.file(workspaceFile).text()
    const patterns: string[] = []
    let inPackagesBlock = false

    for (const line of contents.split(/\r?\n/)) {
      if (/^\s*packages\s*:\s*$/.test(line)) {
        inPackagesBlock = true
        continue
      }

      if (!inPackagesBlock)
        continue

      if (/^\S/.test(line))
        break

      // eslint-disable-next-line regexp/no-super-linear-backtracking
      const match = line.match(/^\s*-\s*['"]?([^'"]+)['"]?\s*$/)
      if (match?.[1])
        patterns.push(match[1])
    }

    return patterns
  }
  catch {
    return []
  }
}

async function collectWorkspacePatterns(root: string): Promise<{ patterns: string[], reasons: string[] }> {
  const reasons: string[] = []
  const pkg = await readJsonFile(path.join(root, 'package.json'))
  const patterns = new Set<string>()

  for (const pattern of workspacePatternsFromPackageJson(pkg))
    patterns.add(pattern)

  if (patterns.size > 0)
    reasons.push('package.json workspaces')

  const pnpmPatterns = await readPnpmWorkspacePatterns(root)
  for (const pattern of pnpmPatterns)
    patterns.add(pattern)

  if (pnpmPatterns.length > 0)
    reasons.push('pnpm-workspace.yaml')

  if (await pathExists(path.join(root, 'turbo.json')))
    reasons.push('turbo.json')

  if (await pathExists(path.join(root, 'nx.json')))
    reasons.push('nx.json')

  if (await isDirectory(path.join(root, 'packages'))) {
    patterns.add('packages/*')
    reasons.push('packages/* layout')
  }

  return {
    patterns: uniqueStrings([...patterns]),
    reasons: uniqueStrings(reasons),
  }
}

async function findWorkspacePackages(root: string, patterns: string[]): Promise<PackageSelection[]> {
  const seen = new Set<string>()
  const packages: PackageSelection[] = []

  for (const pattern of patterns) {
    const globPattern = `${normalizeGlobPattern(pattern)}/package.json`
    const glob = new Bun.Glob(globPattern)

    for await (const file of glob.scan({ cwd: root, onlyFiles: true })) {
      const packageRoot = path.join(root, path.dirname(file))
      if (seen.has(packageRoot))
        continue

      seen.add(packageRoot)
      const pkg = await readJsonFile(path.join(packageRoot, 'package.json'))
      const rawName = typeof pkg?.name === 'string' ? pkg.name : undefined
      packages.push({
        root: packageRoot,
        packageName: rawName,
      })
    }
  }

  return packages.sort((left, right) => {
    const leftLabel = left.packageName ?? path.basename(left.root)
    const rightLabel = right.packageName ?? path.basename(right.root)
    return leftLabel.localeCompare(rightLabel)
  })
}

async function resolveRootPackage(root: string): Promise<PackageSelection> {
  const pkg = await readJsonFile(path.join(root, 'package.json'))
  return {
    root,
    packageName: typeof pkg?.name === 'string' ? pkg.name : undefined,
  }
}

function packageHasDependency(pkg: Record<string, unknown> | null, name: string): boolean {
  const dependencyFields = ['dependencies', 'devDependencies', 'peerDependencies']

  return dependencyFields.some((field) => {
    const record = toRecord(pkg?.[field])
    return Boolean(record && typeof record[name] === 'string')
  })
}

function packageScriptsContain(pkg: Record<string, unknown> | null, token: string): boolean {
  const scripts = toRecord(pkg?.scripts)
  if (!scripts)
    return false

  return Object.values(scripts).some(value => typeof value === 'string' && value.includes(token))
}

async function firstExistingRelativePath(root: string, candidates: string[]): Promise<string | null> {
  for (const candidate of candidates) {
    if (await pathExists(path.join(root, candidate)))
      return candidate
  }

  return null
}

async function detectProjectSignals(packageRoot: string, pkg?: Record<string, unknown> | null): Promise<ProjectSignals> {
  const packageJson = pkg ?? await readJsonFile(path.join(packageRoot, 'package.json'))
  const hasIndexHtml = await pathExists(path.join(packageRoot, 'index.html'))
  const reasons: string[] = []

  const viteConfig = await firstExistingRelativePath(packageRoot, VITE_CONFIG_FILES)
  const webpackConfig = await firstExistingRelativePath(packageRoot, WEBPACK_CONFIG_FILES)
  const uniappConfig = await firstExistingRelativePath(packageRoot, UNIAPP_SIGNAL_FILES)

  const hasViteSignals = Boolean(viteConfig)
    || packageHasDependency(packageJson, 'vite')
    || packageScriptsContain(packageJson, 'vite')

  const hasWebpackSignals = Boolean(webpackConfig)
    || packageHasDependency(packageJson, 'webpack')
    || packageScriptsContain(packageJson, 'webpack')

  const hasUniappSignals = Boolean(uniappConfig)
    || packageHasDependency(packageJson, '@dcloudio/vite-plugin-uni')
    || packageHasDependency(packageJson, '@dcloudio/uni-app')
    || packageScriptsContain(packageJson, 'uni')

  if (hasUniappSignals) {
    reasons.push(uniappConfig ? `found ${uniappConfig}` : 'found uniapp dependencies or scripts')
    return { kind: 'uniapp-h5', reasons, hasIndexHtml }
  }

  if (hasViteSignals) {
    reasons.push(viteConfig ? `found ${viteConfig}` : 'found vite dependencies or scripts')
    return { kind: 'vite', reasons, hasIndexHtml }
  }

  if (hasWebpackSignals) {
    reasons.push(webpackConfig ? `found ${webpackConfig}` : 'found webpack dependencies or scripts')
    return { kind: 'webpack', reasons, hasIndexHtml }
  }

  if (hasIndexHtml) {
    reasons.push('package root contains index.html')
    return { kind: 'frontend', reasons, hasIndexHtml }
  }

  reasons.push('no strong frontend build signal found')
  return { kind: 'generic', reasons, hasIndexHtml }
}

async function scanDirectories(root: string, maxDepth: number): Promise<ScannedDirectory[]> {
  const scanned: ScannedDirectory[] = []
  const queue: Array<{ absolute: string, depth: number }> = [{ absolute: root, depth: 0 }]

  while (queue.length > 0) {
    const current = queue.shift()
    if (!current || current.depth >= maxDepth)
      continue

    let entries
    try {
      entries = await fs.readdir(current.absolute, { withFileTypes: true })
    }
    catch {
      continue
    }

    for (const entry of entries) {
      if (!entry.isDirectory())
        continue

      if (SCAN_IGNORED_DIRS.has(entry.name))
        continue

      const absolute = path.join(current.absolute, entry.name)
      scanned.push({
        absolute,
        relative: normalizeRelativePath(root, absolute),
        depth: current.depth + 1,
      })

      queue.push({
        absolute,
        depth: current.depth + 1,
      })
    }
  }

  return scanned
}

function artifactSpecApplies(spec: ArtifactSpec, kind: ProjectKind): boolean {
  return spec.appliesTo.includes(kind)
}

async function scoreArtifactDirectory(packageRoot: string, absoluteDir: string, relativeDir: string, kind: ProjectKind): Promise<CandidateDirectory> {
  let score = 0
  const reasons: string[] = []

  const nameParts = relativeDir.split('/').filter(Boolean)
  for (const part of nameParts) {
    const partScore = KNOWN_DIR_NAME_SCORES[part]
    if (!partScore)
      continue

    score += partScore
    reasons.push(`matched directory name ${part}`)
  }

  if (await pathExists(path.join(absoluteDir, 'index.html'))) {
    score += 24
    reasons.push('contains index.html')
  }

  if (kind === 'vite' && relativeDir === 'dist') {
    score += 18
    reasons.push('matches vite output convention')
  }

  if (kind === 'webpack' && ['dist', 'build'].includes(relativeDir)) {
    score += 16
    reasons.push('matches webpack output convention')
  }

  if (kind === 'uniapp-h5' && relativeDir.endsWith('/h5')) {
    score += 28
    reasons.push('matches uniapp h5 output convention')
  }

  if (relativeDir.startsWith('dist/') || relativeDir.startsWith('unpackage/')) {
    score += 8
    reasons.push('nested under a common output tree')
  }

  if (absoluteDir === packageRoot) {
    score += 8
    reasons.push('package root fallback')
  }

  return {
    absolute: absoluteDir,
    relative: relativeDir,
    score,
    reasons: uniqueStrings(reasons),
  }
}

async function buildDirectoryCandidates(packageRoot: string, signals: ProjectSignals): Promise<CandidateDirectory[]> {
  const candidateMap = new Map<string, CandidateDirectory>()

  for (const spec of ARTIFACT_DIRECTORY_SPECS) {
    if (!artifactSpecApplies(spec, signals.kind))
      continue

    const absolute = path.join(packageRoot, spec.relative)
    if (!(await isDirectory(absolute)))
      continue

    const candidate = await scoreArtifactDirectory(packageRoot, absolute, spec.relative, signals.kind)
    candidate.score += spec.baseScore
    candidate.reasons = mergeReasonLists(candidate.reasons, [spec.reason])
    addCandidate(candidateMap, candidate)
  }

  const scannedDirs = await scanDirectories(packageRoot, 2)

  for (const scanned of scannedDirs) {
    const hasIndexHtml = await pathExists(path.join(scanned.absolute, 'index.html'))
    const basename = path.basename(scanned.absolute)
    const knownNameScore = KNOWN_DIR_NAME_SCORES[basename] ?? 0

    if (!hasIndexHtml && knownNameScore === 0)
      continue

    const candidate = await scoreArtifactDirectory(packageRoot, scanned.absolute, scanned.relative, signals.kind)
    candidate.score += scanned.depth === 1 ? 10 : 4

    if (!hasIndexHtml && knownNameScore > 0)
      candidate.reasons = mergeReasonLists(candidate.reasons, ['surface-level candidate for manual review'])

    addCandidate(candidateMap, candidate)
  }

  const rootCandidate = await scoreArtifactDirectory(packageRoot, packageRoot, '.', signals.kind)
  if (signals.hasIndexHtml)
    rootCandidate.score += 20

  addCandidate(candidateMap, rootCandidate)

  let candidates = [...candidateMap.values()].sort((left, right) => {
    if (right.score !== left.score)
      return right.score - left.score

    return left.relative.localeCompare(right.relative)
  })

  if (candidates.length <= 1) {
    for (const scanned of scannedDirs.filter(dir => dir.depth <= 1).slice(0, 10)) {
      addCandidate(candidateMap, {
        absolute: scanned.absolute,
        relative: scanned.relative,
        score: 12,
        reasons: ['fallback candidate for manual selection'],
      })
    }

    candidates = [...candidateMap.values()].sort((left, right) => {
      if (right.score !== left.score)
        return right.score - left.score

      return left.relative.localeCompare(right.relative)
    })
  }

  return candidates
}

function deriveRecommendationConfidence(candidates: CandidateDirectory[]): RecommendationConfidence {
  const [recommended, runnerUp] = candidates
  if (!recommended)
    return 'low'

  const gap = runnerUp ? recommended.score - runnerUp.score : recommended.score

  if (recommended.score >= 92 && gap >= 18)
    return 'high'

  if (recommended.score >= 78 && gap >= 8)
    return 'medium'

  return 'low'
}

export async function inspectWorkspaceRoot(root: string): Promise<WorkspaceInspection> {
  const resolvedRoot = path.resolve(root)
  const { patterns, reasons } = await collectWorkspacePatterns(resolvedRoot)
  const packages = patterns.length > 0
    ? await findWorkspacePackages(resolvedRoot, patterns)
    : []

  return {
    root: resolvedRoot,
    reasons,
    packages,
    defaultPackage: await resolveRootPackage(resolvedRoot),
  }
}

export async function buildSourceSelectionModel(packageRoot: string): Promise<SourceSelectionModel> {
  const resolvedRoot = path.resolve(packageRoot)
  const pkg = await readJsonFile(path.join(resolvedRoot, 'package.json'))
  const packageName = typeof pkg?.name === 'string' ? pkg.name : undefined
  const projectSignals = await detectProjectSignals(resolvedRoot, pkg)
  const candidates = await buildDirectoryCandidates(resolvedRoot, projectSignals)
  const recommended = candidates[0]

  if (!recommended)
    throw new Error('No directory candidates found to zip.')

  return {
    packageRoot: resolvedRoot,
    packageName,
    projectSignals,
    candidates,
    recommended,
    confidence: deriveRecommendationConfidence(candidates),
  }
}

export type {
  CandidateDirectory,
  PackageSelection,
  ProjectKind,
  ProjectSignals,
  RecommendationConfidence,
  SourceSelectionModel,
  WorkspaceInspection,
}
