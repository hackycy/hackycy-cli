import type {
  EvidenceCoverage,
  EvidenceFact,
  FileRole,
  GitChangeSnapshot,
  SnapshotFile,
} from './types'
import path from 'node:path'

export const TARGET_LOCAL_PROMPT_TOKENS = 3_000
export const MAX_LOCAL_PROMPT_TOKENS = 4_000

const DIRECTORY_CONTEXT_TOKEN_BUDGET = 600

interface ChangeCluster {
  key: string
  files: SnapshotFile[]
}

interface DirectorySummary {
  path: string
  files: number
  roles: Map<FileRole, number>
  additions: number
  deletions: number
  renamedFrom: number
  renamedTo: number
}

interface DirectoryContext {
  lines: string[]
  compacted: boolean
}

type CommitTypeHint = 'build' | 'chore' | 'ci' | 'docs' | 'style' | 'test'

export interface CompiledEvidence {
  text: string
  coverage: EvidenceCoverage
  facts: EvidenceFact[]
}

function estimateTextTokens(text: string): number {
  let ascii = 0
  let nonAscii = 0
  for (const codePoint of text) {
    if ((codePoint.codePointAt(0) ?? 0) <= 0x7F)
      ascii += 1
    else
      nonAscii += 1
  }
  return Math.ceil(ascii / 2) + nonAscii
}

export function estimateLocalPromptTokens(system: string, evidence: string): number {
  return estimateTextTokens(JSON.stringify([
    { role: 'system', content: system },
    { role: 'user', content: evidence },
  ])) + 32
}

function directory(filePath: string): string {
  const normalized = filePath.replaceAll('\\', '/')
  const separator = normalized.lastIndexOf('/')
  return separator === -1 ? '.' : normalized.slice(0, separator)
}

function directoryParent(directoryPath: string): string | undefined {
  if (directoryPath === '.')
    return undefined
  const separator = directoryPath.lastIndexOf('/')
  return separator === -1 ? '.' : directoryPath.slice(0, separator)
}

function directoryParts(directoryPath: string): string[] {
  return directoryPath === '.' ? [] : directoryPath.split('/')
}

function createDirectorySummary(directoryPath: string): DirectorySummary {
  return {
    path: directoryPath,
    files: 0,
    roles: new Map(),
    additions: 0,
    deletions: 0,
    renamedFrom: 0,
    renamedTo: 0,
  }
}

function addDirectoryFile(summary: DirectorySummary, file: SnapshotFile, rename: 'from' | 'to' | undefined = undefined): void {
  summary.files += 1
  summary.roles.set(file.role, (summary.roles.get(file.role) ?? 0) + 1)
  summary.additions += file.stats.additions
  summary.deletions += file.stats.deletions
  if (rename === 'from')
    summary.renamedFrom += 1
  if (rename === 'to')
    summary.renamedTo += 1
}

function addRenameSource(summary: DirectorySummary): void {
  summary.renamedFrom += 1
}

function mergeDirectorySummary(target: DirectorySummary, source: DirectorySummary): void {
  target.files += source.files
  for (const [role, count] of source.roles)
    target.roles.set(role, (target.roles.get(role) ?? 0) + count)
  target.additions += source.additions
  target.deletions += source.deletions
  target.renamedFrom += source.renamedFrom
  target.renamedTo += source.renamedTo
}

function directoryWeight(summary: DirectorySummary): number {
  const sourceFiles = (summary.roles.get('source') ?? 0) + (summary.roles.get('test') ?? 0)
  const supportingFiles = summary.files - sourceFiles
  return sourceFiles * 4 + supportingFiles + Math.ceil((summary.additions + summary.deletions) / 100)
}

function directoryRoleSummary(summary: DirectorySummary): string {
  return [...summary.roles]
    .toSorted(([left], [right]) => left.localeCompare(right))
    .map(([role, count]) => `${role}:${count}`)
    .join(',')
}

function renderDirectorySummary(summary: DirectorySummary): string {
  const pathLabel = summary.path === '.' ? './' : `${summary.path.slice(summary.path.lastIndexOf('/') + 1)}/`
  const details = summary.files > 0
    ? ` files=${summary.files} roles=${directoryRoleSummary(summary)} +${summary.additions} -${summary.deletions}`
    : ''
  const renames = summary.renamedFrom || summary.renamedTo
    ? ` rename-from=${summary.renamedFrom} rename-to=${summary.renamedTo}`
    : ''
  return `${pathLabel}${details}${renames}`.trimEnd()
}

function renderDirectoryLines(summaries: Map<string, DirectorySummary>): string[] {
  const paths = new Set<string>()
  for (const directoryPath of summaries.keys()) {
    let current: string | undefined = directoryPath
    while (current !== undefined) {
      paths.add(current)
      current = directoryParent(current)
    }
  }
  return [...paths]
    .toSorted((left, right) => directoryParts(left).length - directoryParts(right).length || left.localeCompare(right))
    .map((directoryPath) => {
      const indent = '  '.repeat(directoryParts(directoryPath).length)
      const summary = summaries.get(directoryPath)
      const segment = directoryPath === '.' ? './' : `${directoryPath.slice(directoryPath.lastIndexOf('/') + 1)}/`
      return `${indent}${summary ? renderDirectorySummary(summary) : segment}`
    })
}

function directoryContextTokens(lines: string[]): number {
  return estimateTextTokens(lines.join('\n'))
}

function chooseDirectoryMerge(summaries: Map<string, DirectorySummary>): { parent: string, children: string[] } | undefined {
  const candidates: Array<{ parent: string, child: string, weight: number, depth: number }> = []
  for (const directoryPath of summaries.keys()) {
    const parent = directoryParent(directoryPath)
    if (parent === undefined)
      continue
    candidates.push({
      parent,
      child: directoryPath,
      weight: directoryWeight(summaries.get(directoryPath)!),
      depth: directoryParts(parent).length,
    })
  }
  candidates.sort((left, right) => left.weight - right.weight || right.depth - left.depth || left.parent.localeCompare(right.parent) || left.child.localeCompare(right.child))
  const selected = candidates[0]
  if (!selected)
    return undefined
  return { parent: selected.parent, children: [selected.child] }
}

function compactDirectorySummaries(summaries: Map<string, DirectorySummary>): boolean {
  let compacted = false
  while (directoryContextTokens(['DIRECTORY_CONTEXT', ...renderDirectoryLines(summaries)]) > DIRECTORY_CONTEXT_TOKEN_BUDGET) {
    const merge = chooseDirectoryMerge(summaries)
    if (!merge)
      break
    const parent = summaries.get(merge.parent) ?? createDirectorySummary(merge.parent)
    for (const child of merge.children) {
      mergeDirectorySummary(parent, summaries.get(child)!)
      summaries.delete(child)
    }
    summaries.set(merge.parent, parent)
    compacted = true
  }
  return compacted
}

function buildDirectoryContext(snapshot: GitChangeSnapshot): DirectoryContext {
  const summaries = new Map<string, DirectorySummary>()
  const inspectable = snapshot.files.filter(file => file.contentPolicy === 'inspect')
  for (const file of inspectable) {
    const targetDirectory = directory(file.path)
    const target = summaries.get(targetDirectory) ?? createDirectorySummary(targetDirectory)
    addDirectoryFile(target, file, file.originalPath ? 'to' : undefined)
    summaries.set(targetDirectory, target)
    if (file.originalPath) {
      const originalDirectory = directory(file.originalPath)
      const original = summaries.get(originalDirectory) ?? createDirectorySummary(originalDirectory)
      addRenameSource(original)
      summaries.set(originalDirectory, original)
    }
  }
  const compacted = compactDirectorySummaries(summaries)
  const lines = ['DIRECTORY_CONTEXT']
  lines.push(...(summaries.size > 0 ? renderDirectoryLines(summaries) : ['(no inspectable changed directories)']))
  return { lines, compacted }
}

function moduleRoot(filePath: string): string | undefined {
  const normalized = filePath.replaceAll('\\', '/')
  const match = /^(?:packages|apps|services|crates)\/[^/]+|^src\/commands\/[^/]+/.exec(normalized)
  return match?.[0]
}

function commonDirectoryDepth(left: string, right: string): number {
  const leftParts = left === '.' ? [] : left.split('/')
  const rightParts = right === '.' ? [] : right.split('/')
  let depth = 0
  while (leftParts[depth] && leftParts[depth] === rightParts[depth])
    depth += 1
  return depth
}

function initialClusterKey(file: SnapshotFile): string {
  return moduleRoot(file.path) ?? directory(file.path)
}

function attachSupportingFile(file: SnapshotFile, existingKeys: string[]): string {
  const fileDirectory = directory(file.path)
  const candidates = existingKeys
    .map(key => ({ key, depth: commonDirectoryDepth(fileDirectory, key) }))
    .filter(candidate => candidate.depth > 0)
    .toSorted((left, right) => right.depth - left.depth || left.key.localeCompare(right.key))
  return candidates[0]?.key ?? initialClusterKey(file)
}

export function buildChangeClusters(snapshot: GitChangeSnapshot): ChangeCluster[] {
  const primary = snapshot.files.filter(file => !['config', 'dependency', 'docs'].includes(file.role))
  const keys = new Map<string, SnapshotFile[]>()
  for (const file of primary) {
    const key = initialClusterKey(file)
    const files = keys.get(key) ?? []
    files.push(file)
    keys.set(key, files)
  }
  const existingKeys = [...keys.keys()].sort()
  for (const file of snapshot.files.filter(file => !primary.includes(file))) {
    const key = attachSupportingFile(file, existingKeys)
    const files = keys.get(key) ?? []
    files.push(file)
    keys.set(key, files)
    if (!existingKeys.includes(key))
      existingKeys.push(key)
  }
  return [...keys]
    .map(([key, files]) => ({ key, files: files.toSorted((left, right) => left.path.localeCompare(right.path)) }))
    .toSorted((left, right) => left.key.localeCompare(right.key))
}

function roleRank(role: FileRole): number {
  if (role === 'dependency' || role === 'config')
    return 2
  if (role === 'test')
    return 3
  if (role === 'generated' || role === 'binary' || role === 'sensitive')
    return 6
  if (role === 'source')
    return 4
  return 5
}

function fact(
  priority: EvidenceFact['priority'],
  clusterKey: string,
  filePath: string,
  suffix: string,
  text: string,
  hunkId?: string,
): EvidenceFact {
  return { id: `${priority}:${clusterKey}:${filePath}:${suffix}`, priority, clusterKey, filePath, hunkId, text }
}

function declarationName(line: string): string | undefined {
  const match = /\bexport\s+(?:default\s+)?(?:async\s+)?(?:class|interface|type|enum|function|def|func|struct|const|let|var)\s+([A-Za-z_$][\w$]*)/.exec(line)
  return match?.[1]
}

function testName(line: string): string | undefined {
  const match = /\b(?:describe|test|it)\s*\(\s*['"`]([^'"`]+)['"`]/.exec(line)
  return match?.[1]
}

function documentationHeading(line: string): string | undefined {
  const match = /^#{1,6} (.+)$/.exec(line.trim())
  return match?.[1]?.trim()
}

function configurationKey(line: string): string | undefined {
  const match = /^\s*["']?([a-z_$][\w$.-]*)["']?\s*[:=]/i.exec(line)
  return match?.[1]
}

function meaningfulLine(line: string): boolean {
  return Boolean(line.trim()) && !/^[{});,]+$/.test(line.trim())
}

function isBehaviorLine(line: string): boolean {
  return /throw\s+new|\bError\(|--[\w-]+|\b(?:error|message|title|description|help)\b/i.test(line)
}

function hunkContext(line: string): string | undefined {
  const match = /\b(?:async\s+)?(class|interface|type|function|def|func|struct)\s+([A-Za-z_$][\w$]*)/.exec(line)
  return match ? `context ${match[1]} ${match[2]}` : undefined
}

function isLowInformationLine(line: string): boolean {
  return /^(?:import|export\s*\{).*|^\/\/|^\*|^[{});,]+$/.test(line.trim())
}

function changeKind(file: SnapshotFile, line: string): 'added' | 'removed' {
  return file.hunks.some(hunk => hunk.addedLines.includes(line)) ? 'added' : 'removed'
}

function packageFacts(file: SnapshotFile, clusterKey: string): EvidenceFact[] {
  const manifest = file.manifest
  if (!manifest)
    return []
  if (!manifest.before)
    return [fact(1, clusterKey, file.path, 'package-added', 'package manifest added')]
  if (!manifest.after)
    return [fact(1, clusterKey, file.path, 'package-removed', 'package manifest removed')]
  try {
    const before = JSON.parse(manifest.before) as Record<string, unknown>
    const after = JSON.parse(manifest.after) as Record<string, unknown>
    const facts: EvidenceFact[] = []
    for (const field of ['dependencies', 'devDependencies'] as const) {
      const beforeDependencies = (before[field] ?? {}) as Record<string, string>
      const afterDependencies = (after[field] ?? {}) as Record<string, string>
      const names = new Set([...Object.keys(beforeDependencies), ...Object.keys(afterDependencies)])
      const added: string[] = []
      const removed: string[] = []
      const changed: string[] = []
      for (const name of [...names].sort()) {
        if (beforeDependencies[name] === afterDependencies[name])
          continue
        if (!beforeDependencies[name])
          added.push(`${name}@${afterDependencies[name]}`)
        else if (!afterDependencies[name])
          removed.push(`${name}@${beforeDependencies[name]}`)
        else
          changed.push(`${name} ${beforeDependencies[name]} -> ${afterDependencies[name]}`)
      }
      if (added.length && removed.length) {
        facts.push(fact(1, clusterKey, file.path, `dependency:${field}:replacement`, `dependency replacement add ${added.join(', ')}; remove ${removed.join(', ')}`))
      }
      else {
        if (added.length)
          facts.push(fact(1, clusterKey, file.path, `dependency:${field}:added`, `dependency added ${added.join(', ')}`))
        if (removed.length)
          facts.push(fact(1, clusterKey, file.path, `dependency:${field}:removed`, `dependency removed ${removed.join(', ')}`))
      }
      if (changed.length)
        facts.push(fact(1, clusterKey, file.path, `dependency:${field}:changed`, `dependency updated ${changed.join(', ')}`))
    }
    for (const field of ['scripts', 'name', 'version', 'type', 'exports', 'bin', 'engines'] as const) {
      if (JSON.stringify(before[field]) !== JSON.stringify(after[field])) {
        facts.push(fact(
          1,
          clusterKey,
          file.path,
          `manifest:${field}`,
          field === 'version' ? `release chore ${String(before.version)}->${String(after.version)}` : `package ${field} changed`,
        ))
      }
    }
    return facts.length > 0 ? facts : [fact(1, clusterKey, file.path, 'package-generic', 'package manifest changed')]
  }
  catch {
    return [fact(1, clusterKey, file.path, 'package-unparsed', 'package manifest changed')]
  }
}

export function extractEvidenceFacts(clusters: ChangeCluster[]): EvidenceFact[] {
  const facts: EvidenceFact[] = []
  for (const cluster of clusters) {
    for (const file of cluster.files) {
      if (file.contentPolicy !== 'inspect')
        continue
      const factsBeforeFile = facts.length
      if (file.role === 'dependency') {
        facts.push(...packageFacts(file, cluster.key))
        continue
      }
      if (file.originalPath && (file.indexStatus === 'R' || file.indexStatus === 'C')) {
        facts.push(fact(1, cluster.key, file.path, 'rename', `${file.indexStatus === 'R' ? 'rename' : 'copy'} from ${file.originalPath}`))
      }
      let includedHeading = false
      let includedDeclaration = false
      let includedTest = false
      let includedDocumentationHeading = false
      for (const hunk of file.hunks) {
        const context = hunk.heading && file.role !== 'test' ? hunkContext(hunk.heading) : undefined
        if (context && !includedHeading) {
          facts.push(fact(1, cluster.key, file.path, `heading:${hunk.id}`, context, hunk.id))
          includedHeading = true
        }
        for (const line of [...hunk.addedLines, ...hunk.deletedLines]) {
          const kind = changeKind(file, line)
          const declaration = declarationName(line)
          if (declaration && file.role !== 'test' && !includedDeclaration) {
            facts.push(fact(1, cluster.key, file.path, `declaration:${hunk.id}:${declaration}`, `symbol ${kind} ${declaration}`, hunk.id))
            includedDeclaration = true
          }
          const test = testName(line)
          if (test && !includedTest) {
            facts.push(fact(1, cluster.key, file.path, `test:${hunk.id}:${test}`, `test ${kind} ${JSON.stringify(test)}`, hunk.id))
            includedTest = true
          }
          const heading = file.role === 'docs' ? documentationHeading(line) : undefined
          if (heading && !includedDocumentationHeading) {
            facts.push(fact(1, cluster.key, file.path, `docs:${hunk.id}:${heading}`, `docs ${kind} ${JSON.stringify(heading)}`, hunk.id))
            includedDocumentationHeading = true
          }
          const key = file.role === 'config' ? configurationKey(line) : undefined
          if (key)
            facts.push(fact(1, cluster.key, file.path, `config:${hunk.id}:${key}`, `config ${kind} ${key}`, hunk.id))
        }
        if (file.role === 'test' || file.role === 'docs')
          continue
        const candidateLines = [
          ...hunk.addedLines.map(line => ({ kind: 'added', line })),
          ...hunk.deletedLines.map(line => ({ kind: 'removed', line })),
        ].filter(candidate => meaningfulLine(candidate.line) && !isLowInformationLine(candidate.line))
        let includedBehavior = false
        for (const [index, candidate] of candidateLines.entries()) {
          const trimmed = candidate.line.trim()
          if (declarationName(trimmed) || testName(trimmed))
            continue
          const priority: EvidenceFact['priority'] = isBehaviorLine(trimmed) ? 2 : 3
          if (priority === 2 && includedBehavior)
            continue
          includedBehavior ||= priority === 2
          facts.push(fact(priority, cluster.key, file.path, `line:${hunk.id}:${index}`, `${candidate.kind} ${trimmed}`, hunk.id))
        }
      }
      if (!facts.slice(factsBeforeFile).some(item => item.priority < 3))
        facts.push(fact(1, cluster.key, file.path, 'file', 'file changed'))
    }
  }
  const unique = new Map<string, EvidenceFact>()
  for (const item of facts)
    unique.set(item.id, item)
  return [...unique.values()]
    .toSorted((left, right) => left.priority - right.priority
      || left.clusterKey.localeCompare(right.clusterKey)
      || left.filePath.localeCompare(right.filePath)
      || (left.hunkId ?? '').localeCompare(right.hunkId ?? '')
      || left.id.localeCompare(right.id))
}

function protectionCounts(snapshot: GitChangeSnapshot): { metadataOnly: number, redacted: number } {
  return {
    metadataOnly: snapshot.files.filter(file => file.contentPolicy === 'metadata-only').length,
    redacted: snapshot.files.filter(file => file.contentPolicy === 'redacted').length,
  }
}

function packageVersionChanged(file: SnapshotFile): boolean {
  if (path.basename(file.path) !== 'package.json' || !file.manifest?.before || !file.manifest.after)
    return false
  try {
    const before = JSON.parse(file.manifest.before) as { version?: unknown }
    const after = JSON.parse(file.manifest.after) as { version?: unknown }
    return before.version !== after.version
  }
  catch {
    return false
  }
}

function commitTypeHint(snapshot: GitChangeSnapshot): CommitTypeHint | undefined {
  const files = snapshot.files
  const paths = files.map(file => file.path)
  if (paths.some(filePath => filePath.startsWith('.github/workflows/')))
    return 'ci'
  if (paths.some(filePath => path.basename(filePath).toLowerCase() === 'dockerfile'))
    return 'build'
  if (files.length > 0 && files.every(file => file.role === 'config' || /(?:^|\/)tsconfig(?:\.|$)|\.d\.ts$/i.test(file.path)))
    return 'build'
  if (files.every(file => file.role === 'docs'))
    return 'docs'
  if (paths.every(filePath => /\.(?:css|scss|less)$/i.test(filePath)))
    return 'style'
  if (files.every(file => file.role === 'test'))
    return 'test'
  if (files.some(packageVersionChanged) && files.every(file => file.role === 'dependency' || file.role === 'generated'))
    return 'chore'
  if (files.every(file => file.role === 'dependency' || file.role === 'generated'))
    return 'chore'
  return undefined
}

function renderScope(snapshot: GitChangeSnapshot): string {
  const typeHint = commitTypeHint(snapshot)
  const { metadataOnly, redacted } = protectionCounts(snapshot)
  const summary = `CHANGE_SUMMARY files=${snapshot.files.length} +${snapshot.totals.additions} -${snapshot.totals.deletions} protected=${metadataOnly}/${redacted}`
  return typeHint ? `${summary} type=${typeHint}` : summary
}

function renderP0(snapshot: GitChangeSnapshot, directoryContext: DirectoryContext): string[] {
  return [
    renderScope(snapshot),
    ...directoryContext.lines,
  ]
}

function sortClustersForFacts(clusters: ChangeCluster[], facts: EvidenceFact[]): ChangeCluster[] {
  const priorityByCluster = new Map<string, number>()
  for (const cluster of clusters) {
    const factPriority = facts.filter(fact => fact.clusterKey === cluster.key).reduce((lowest, fact) => Math.min(lowest, fact.priority), 10)
    const rolePriority = Math.min(...cluster.files.map(file => roleRank(file.role)))
    priorityByCluster.set(cluster.key, Math.min(factPriority, rolePriority))
  }
  return clusters.toSorted((left, right) => (priorityByCluster.get(left.key) ?? 10) - (priorityByCluster.get(right.key) ?? 10)
    || left.key.localeCompare(right.key))
}

function factRank(item: EvidenceFact): number {
  if (item.text.startsWith('rename ') || item.text.startsWith('copy ') || item.text.startsWith('dependency ') || item.text.startsWith('package ') || item.text.startsWith('config '))
    return 1
  if (item.text.startsWith('symbol '))
    return 2
  if (item.text.startsWith('test '))
    return 3
  if (item.text.startsWith('docs '))
    return 4
  if (item.text.startsWith('context '))
    return 5
  if (/^(?:added|removed)\s+(?:async\s+)?(?:function|class|interface|type|enum|const)\b/.test(item.text))
    return 6
  if (/^(?:added|removed)\s+(?:return|throw)\b/.test(item.text))
    return 8
  return 7
}

function orderFactsForCluster(facts: EvidenceFact[]): EvidenceFact[] {
  const byFile = new Map<string, EvidenceFact[]>()
  for (const item of facts) {
    const fileFacts = byFile.get(item.filePath) ?? []
    fileFacts.push(item)
    byFile.set(item.filePath, fileFacts)
  }
  const queues = [...byFile.entries()]
    .map(([filePath, fileFacts]) => ({
      filePath,
      facts: fileFacts.toSorted((left, right) => factRank(left) - factRank(right)
        || (left.hunkId ?? '').localeCompare(right.hunkId ?? '')
        || left.id.localeCompare(right.id)),
    }))
    .toSorted((left, right) => left.filePath.localeCompare(right.filePath))
  const ordered: EvidenceFact[] = []
  while (queues.some(queue => queue.facts.length > 0)) {
    for (const queue of queues) {
      const item = queue.facts.shift()
      if (item)
        ordered.push(item)
    }
  }
  return ordered
}

function renderSelectedFacts(facts: EvidenceFact[]): string[] {
  if (facts.length === 0)
    return []

  const directories = new Map<string, Map<string, EvidenceFact[]>>()
  for (const item of facts) {
    const directoryPath = directory(item.filePath)
    const files = directories.get(directoryPath) ?? new Map<string, EvidenceFact[]>()
    const fileFacts = files.get(item.filePath) ?? []
    fileFacts.push(item)
    files.set(item.filePath, fileFacts)
    directories.set(directoryPath, files)
  }

  const lines = ['FACTS']
  for (const [directoryPath, files] of [...directories].toSorted(([left], [right]) => left.localeCompare(right))) {
    lines.push(directoryPath === '.' ? './' : `${directoryPath}/`)
    for (const [filePath, fileFacts] of [...files].toSorted(([left], [right]) => left.localeCompare(right))) {
      const fileName = filePath.slice(directoryPath === '.' ? 0 : directoryPath.length + 1)
      if (fileFacts.length === 1) {
        lines.push(`  ${fileName}: ${fileFacts[0]!.text}`)
        continue
      }
      lines.push(`  ${fileName}:`)
      lines.push(...fileFacts.map(item => `    ${item.text}`))
    }
  }
  return lines
}

export function compileEvidence(snapshot: GitChangeSnapshot, system: string): CompiledEvidence {
  const clusters = buildChangeClusters(snapshot)
  const facts = extractEvidenceFacts(clusters)
  const directoryContext = buildDirectoryContext(snapshot)
  const p0 = renderP0(snapshot, directoryContext)

  const selected: EvidenceFact[] = []
  const orderedClusters = sortClustersForFacts(clusters, facts)
  const renderEvidence = (items: EvidenceFact[]): string => [...p0, ...renderSelectedFacts(items)].join('\n')
  const fits = (next: EvidenceFact[]): boolean => estimateLocalPromptTokens(system, renderEvidence(next)) <= MAX_LOCAL_PROMPT_TOKENS
  const priorities = [1, 2, 3] as const
  for (const priority of priorities) {
    const pending = new Map<string, EvidenceFact[]>()
    for (const cluster of orderedClusters) {
      const priorityFacts = facts.filter(fact => fact.clusterKey === cluster.key && fact.priority === priority)
      const limit = priority === 3 ? new Set(priorityFacts.map(fact => fact.filePath)).size : undefined
      pending.set(cluster.key, orderFactsForCluster(priorityFacts).slice(0, limit))
    }
    while ([...pending.values()].some(queue => queue.length > 0)) {
      for (const cluster of orderedClusters) {
        const queue = pending.get(cluster.key)!
        const item = queue.shift()
        if (!item)
          continue
        const aboveTarget = estimateLocalPromptTokens(system, renderEvidence(selected)) >= TARGET_LOCAL_PROMPT_TOKENS
        if (aboveTarget && priority > 1)
          continue
        if (fits([...selected, item]))
          selected.push(item)
      }
    }
  }
  const text = renderEvidence(selected)
  const coverage: EvidenceCoverage = {
    estimatedLocalPromptTokens: estimateLocalPromptTokens(system, text),
    representedClusters: clusters.length,
    totalClusters: clusters.length,
    includedFacts: selected.length,
    omittedFacts: facts.length - selected.length,
    contentCompacted: directoryContext.compacted || selected.length < facts.length,
  }
  return { text, coverage, facts }
}
