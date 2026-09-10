import type { DirectoryEntry, ExtractionTask, OperationResult } from './api'
import type { ActivityTask, SortDirection, SortKey } from './types'

export interface ExplorerSelection {
  paths: string[]
  anchorPath?: string
}

export interface ExplorerClipboard {
  mode: 'copy' | 'move'
  paths: string[]
}

export interface NavigationHistory {
  paths: string[]
  cursor: number
}

export function entryNameError(name: string): string | undefined {
  if (!name.trim())
    return 'Name cannot be empty'
  if (name === '.' || name === '..' || /[\\/\0]/.test(name))
    return 'Name cannot contain path separators or reserved segments'
  return undefined
}

export function renameSelectionEnd(entry: Pick<DirectoryEntry, 'kind' | 'name'>): number {
  if (entry.kind !== 'file')
    return entry.name.length
  const extensionIndex = entry.name.lastIndexOf('.')
  return extensionIndex > 0 ? extensionIndex : entry.name.length
}

export function parentDirectoryPath(entryPath: string): string {
  return entryPath.split('/').slice(0, -1).join('/')
}

export function extractableSelection(entries: DirectoryEntry[], paths: string[]): boolean {
  if (paths.length === 0)
    return false
  const extractable = new Map(entries.map(entry => [entry.path, entry.extractable]))
  return paths.every(path => extractable.get(path) === true)
}

const EXTRACTION_STATUS_RANK: Record<ExtractionTask['status'], number> = {
  queued: 0,
  running: 1,
  done: 2,
  error: 2,
  cancelled: 2,
}

export function mergeExtractionTasks(current: ExtractionTask[], updates: ExtractionTask[]): ExtractionTask[] {
  const currentById = new Map(current.map(task => [task.id, task]))
  const mergedUpdates = updates.map((update) => {
    const previous = currentById.get(update.id)
    if (!previous)
      return update
    const previousRank = EXTRACTION_STATUS_RANK[previous.status]
    const updateRank = EXTRACTION_STATUS_RANK[update.status]
    if (updateRank < previousRank)
      return previous
    if (previousRank === 2 && updateRank === 2) {
      const previousFinished = Date.parse(previous.finishedAt ?? '') || 0
      const updateFinished = Date.parse(update.finishedAt ?? '') || 0
      return updateFinished > previousFinished ? { ...previous, ...update } : previous
    }
    const progress = previous.progress === undefined
      ? update.progress
      : update.progress === undefined
        ? previous.progress
        : Math.max(previous.progress, update.progress)
    return { ...previous, ...update, ...(progress === undefined ? {} : { progress }) }
  })
  const updateIds = new Set(updates.map(task => task.id))
  return [...mergedUpdates, ...current.filter(task => !updateIds.has(task.id))]
}

export function createNavigationHistory(initialPath: string): NavigationHistory {
  return { paths: [initialPath], cursor: 0 }
}

export function pushNavigation(current: NavigationHistory, path: string): NavigationHistory {
  const paths = [...current.paths.slice(0, current.cursor + 1), path]
  return { paths, cursor: paths.length - 1 }
}

export function moveNavigation(current: NavigationHistory, offset: -1 | 1): NavigationHistory {
  return {
    ...current,
    cursor: Math.max(0, Math.min(current.paths.length - 1, current.cursor + offset)),
  }
}

export function clipboardOperation(clipboard: ExplorerClipboard, destinationPath: string): {
  action: 'copy' | 'move'
  paths: string[]
  destinationPath: string
} {
  return { action: clipboard.mode, paths: clipboard.paths, destinationPath }
}

export function settleClipboard(
  clipboard: ExplorerClipboard,
  items: Array<{ status: 'ok' | 'error', sourcePath?: string }>,
): ExplorerClipboard | undefined {
  if (clipboard.mode === 'copy')
    return clipboard
  const moved = new Set(items.filter(item => item.status === 'ok').map(item => item.sourcePath))
  const paths = clipboard.paths.filter(path => !moved.has(path))
  return paths.length > 0 ? { ...clipboard, paths } : undefined
}

export function operationActivities(id: string, label: string, result: OperationResult): ActivityTask[] {
  const verbs: Record<OperationResult['action'], string> = {
    'create-directory': 'Create',
    'rename': 'Rename',
    'copy': 'Copy',
    'move': 'Move',
    'delete': 'Delete',
  }
  return result.items.map((item, index) => {
    const path = item.sourcePath ?? item.destinationPath
    const name = path?.split('/').at(-1)
    const itemLabel = result.items.length === 1 ? label : `${verbs[result.action]} ${name ?? `item ${index + 1}`}`
    if (item.status === 'error') {
      return {
        id: `${id}:${index}`,
        label: itemLabel,
        status: 'error',
        detail: item.error.message,
      }
    }
    const detail = result.action === 'delete'
      ? 'Deleted permanently'
      : item.destinationPath
        ? `${result.action === 'create-directory' ? 'Created' : result.action === 'rename' ? 'Renamed to' : result.action === 'copy' ? 'Copied to' : 'Moved to'} /${item.destinationPath}`
        : 'Complete'
    return {
      id: `${id}:${index}`,
      label: itemLabel,
      status: 'done',
      detail,
    }
  })
}

export function selectEntry(
  order: string[],
  current: ExplorerSelection,
  path: string,
  modifiers: { toggle?: boolean, range?: boolean },
): ExplorerSelection {
  if (modifiers.range && current.anchorPath && order.includes(current.anchorPath)) {
    const anchorIndex = order.indexOf(current.anchorPath)
    const nextIndex = order.indexOf(path)
    const start = Math.min(anchorIndex, nextIndex)
    const end = Math.max(anchorIndex, nextIndex)
    return { paths: order.slice(start, end + 1), anchorPath: current.anchorPath }
  }

  if (modifiers.toggle) {
    const selected = new Set(current.paths)
    selected.has(path) ? selected.delete(path) : selected.add(path)
    return { paths: order.filter(entryPath => selected.has(entryPath)), anchorPath: path }
  }

  return { paths: [path], anchorPath: path }
}

function entryRank(entry: DirectoryEntry): number {
  return entry.kind === 'directory' ? 0 : entry.kind === 'file' ? 1 : 2
}

export function visibleEntries(
  entries: DirectoryEntry[],
  query: string,
  key: SortKey,
  direction: SortDirection,
): DirectoryEntry[] {
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const multiplier = direction === 'asc' ? 1 : -1
  return entries
    .filter(entry => !normalizedQuery || entry.name.toLocaleLowerCase().includes(normalizedQuery))
    .sort((left, right) => {
      const rank = entryRank(left) - entryRank(right)
      if (rank !== 0)
        return rank
      if (key === 'name')
        return multiplier * (left.name.localeCompare(right.name, undefined, { sensitivity: 'base' }) || left.name.localeCompare(right.name))
      if (key === 'size')
        return multiplier * ((left.size ?? -1) - (right.size ?? -1)) || left.name.localeCompare(right.name)
      return multiplier * ((Date.parse(left.modifiedAt ?? '') || 0) - (Date.parse(right.modifiedAt ?? '') || 0)) || left.name.localeCompare(right.name)
    })
}
