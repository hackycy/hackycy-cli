import type { DirectoryEntry } from './api'
import { describe, expect, it } from 'vitest'
import { clipboardOperation, createNavigationHistory, entryNameError, extractableSelection, mergeExtractionTasks, moveNavigation, operationActivities, parentDirectoryPath, pushNavigation, renameSelectionEnd, selectEntry, settleClipboard, visibleEntries } from './explorer-state'

const entries: DirectoryEntry[] = [
  { name: 'zeta.txt', path: 'zeta.txt', kind: 'file', isSymlink: false, previewKind: 'text', size: 8, extractable: false },
  { name: 'Alpha.txt', path: 'Alpha.txt', kind: 'file', isSymlink: false, previewKind: 'text', size: 4, extractable: false },
  { name: 'Alpha docs', path: 'Alpha docs', kind: 'directory', isSymlink: false, previewKind: 'none', extractable: false },
]

describe('explorer state', () => {
  it('validates entry names and selects file names without their final extension', () => {
    expect(entryNameError('')).toBe('Name cannot be empty')
    expect(entryNameError('..')).toBe('Name cannot contain path separators or reserved segments')
    expect(entryNameError('nested/name')).toBe('Name cannot contain path separators or reserved segments')
    expect(entryNameError('report.txt')).toBeUndefined()

    expect(renameSelectionEnd({ kind: 'file', name: 'report.tar.gz' })).toBe('report.tar'.length)
    expect(renameSelectionEnd({ kind: 'file', name: '.env' })).toBe('.env'.length)
    expect(renameSelectionEnd({ kind: 'directory', name: 'reports.2026' })).toBe('reports.2026'.length)
    expect(parentDirectoryPath('docs/reports/file.txt')).toBe('docs/reports')
    expect(parentDirectoryPath('root-file.txt')).toBe('')
  })

  it('filters the current directory by name while preserving directory-first sorting', () => {
    expect(visibleEntries(entries, 'ALPHA', 'name', 'asc').map(entry => entry.path)).toEqual([
      'Alpha docs',
      'Alpha.txt',
    ])
  })

  it('supports plain, additive, and anchored range selection', () => {
    const order = ['Alpha docs', 'Alpha.txt', 'zeta.txt']
    const first = selectEntry(order, { paths: [] }, 'Alpha.txt', {})
    const additive = selectEntry(order, first, 'zeta.txt', { toggle: true })
    const range = selectEntry(order, additive, 'Alpha docs', { range: true })

    expect(first).toEqual({ paths: ['Alpha.txt'], anchorPath: 'Alpha.txt' })
    expect(additive).toEqual({ paths: ['Alpha.txt', 'zeta.txt'], anchorPath: 'zeta.txt' })
    expect(range).toEqual({ paths: ['Alpha docs', 'Alpha.txt', 'zeta.txt'], anchorPath: 'zeta.txt' })
  })

  it('enables extraction only when every selected entry is extractable', () => {
    const archiveEntries: DirectoryEntry[] = [
      ...entries,
      { name: 'one.zip', path: 'one.zip', kind: 'file', isSymlink: false, previewKind: 'none', extractable: true },
      { name: 'two.tar.gz', path: 'two.tar.gz', kind: 'file', isSymlink: false, previewKind: 'none', extractable: true },
    ]

    expect(extractableSelection(archiveEntries, ['one.zip'])).toBe(true)
    expect(extractableSelection(archiveEntries, ['one.zip', 'two.tar.gz'])).toBe(true)
    expect(extractableSelection(archiveEntries, ['one.zip', 'Alpha.txt'])).toBe(false)
    expect(extractableSelection(archiveEntries, [])).toBe(false)
    expect(extractableSelection(archiveEntries, ['missing.zip'])).toBe(false)
  })

  it('does not regress extraction state when a stale request response arrives after SSE', () => {
    const completed = {
      id: 'extract',
      archivePath: 'backup.zip',
      destinationPath: 'backup',
      status: 'done' as const,
      progress: 100,
      uncompressedBytes: 17,
      entryCount: 1,
      createdAt: '2026-01-01T00:00:00.000Z',
      startedAt: '2026-01-01T00:00:01.000Z',
      finishedAt: '2026-01-01T00:00:02.000Z',
    }

    expect(mergeExtractionTasks([completed], [{
      id: 'extract',
      archivePath: 'backup.zip',
      status: 'running',
      createdAt: completed.createdAt,
      startedAt: completed.startedAt,
    }])).toEqual([completed])

    expect(mergeExtractionTasks([{ ...completed, status: 'running', progress: 60, destinationPath: undefined, finishedAt: undefined }], [{
      id: 'extract',
      archivePath: 'backup.zip',
      status: 'running',
      progress: 20,
      createdAt: completed.createdAt,
      startedAt: completed.startedAt,
    }])[0]).toMatchObject({ progress: 60, uncompressedBytes: 17, entryCount: 1 })
  })

  it('creates paste operations and only removes successfully moved clipboard items', () => {
    const copy = { mode: 'copy' as const, paths: ['Alpha.txt'] }
    const move = { mode: 'move' as const, paths: ['Alpha.txt', 'zeta.txt'] }

    expect(clipboardOperation(copy, 'docs')).toEqual({ action: 'copy', paths: ['Alpha.txt'], destinationPath: 'docs' })
    expect(settleClipboard(copy, [{ status: 'ok', sourcePath: 'Alpha.txt' }])).toEqual(copy)
    expect(settleClipboard(move, [
      { status: 'ok', sourcePath: 'Alpha.txt' },
      { status: 'error', sourcePath: 'zeta.txt' },
    ])).toEqual({ mode: 'move', paths: ['zeta.txt'] })
  })

  it('turns batch operation results into per-item activity entries', () => {
    expect(operationActivities('activity', 'Copy items', {
      version: 1,
      action: 'copy',
      items: [
        { status: 'ok', sourcePath: 'Alpha.txt', destinationPath: 'docs/Alpha.txt' },
        { status: 'error', sourcePath: 'archive', error: { code: 'INVALID_OPERATION', message: 'A directory cannot be copied into itself' } },
      ],
    })).toEqual([
      { id: 'activity:0', label: 'Copy Alpha.txt', status: 'done', detail: 'Copied to /docs/Alpha.txt' },
      { id: 'activity:1', label: 'Copy archive', status: 'error', detail: 'A directory cannot be copied into itself' },
    ])

    expect(operationActivities('delete', 'Delete 1 item', {
      version: 1,
      action: 'delete',
      items: [{ status: 'ok', sourcePath: 'obsolete.txt' }],
    })).toEqual([
      { id: 'delete:0', label: 'Delete 1 item', status: 'done', detail: 'Deleted permanently' },
    ])
  })

  it('moves backward and forward while new navigation truncates forward history', () => {
    const root = createNavigationHistory('')
    const nested = pushNavigation(pushNavigation(root, 'docs'), 'docs/api')
    const back = moveNavigation(nested, -1)
    const replacedForward = pushNavigation(back, 'examples')

    expect(back).toEqual({ paths: ['', 'docs', 'docs/api'], cursor: 1 })
    expect(moveNavigation(back, 1).cursor).toBe(2)
    expect(replacedForward).toEqual({ paths: ['', 'docs', 'examples'], cursor: 2 })
  })
})
