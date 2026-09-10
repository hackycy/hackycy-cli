import type { DirectoryEntry, DirectoryListing } from '../api'
import { ChevronRight, CircleAlert, HardDrive, Link as LinkIcon, LoaderCircle, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ScrollArea } from '../../shared/components/ui/scroll-area'
import { cn } from '../../shared/lib/utils'
import { apiJson } from '../api'
import { EntryGlyph } from './entry-glyph'

function ancestorPaths(path: string): string[] {
  const segments = path.split('/').filter(Boolean)
  return ['', ...segments.map((_, index) => segments.slice(0, index + 1).join('/'))]
}

export function NavigationPane({
  rootName,
  currentPath,
  previewPath,
  revision,
  onNavigate,
  onOpenFile,
}: {
  rootName: string
  currentPath: string
  previewPath?: string
  revision: number
  onNavigate: (path: string) => void
  onOpenFile: (entry: DirectoryEntry) => void
}): React.JSX.Element {
  const [entriesByPath, setEntriesByPath] = useState<Record<string, DirectoryEntry[] | undefined>>({})
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['']))
  const [loadedRevisionByPath, setLoadedRevisionByPath] = useState<Record<string, number | undefined>>({})
  const [loadingRevisionByPath, setLoadingRevisionByPath] = useState<Record<string, number | undefined>>({})
  const [errors, setErrors] = useState<Record<string, { revision: number, message: string } | undefined>>({})
  const revisionRef = useRef(revision)
  revisionRef.current = revision

  const load = useCallback(async (directoryPath: string, requestRevision: number): Promise<void> => {
    setLoadingRevisionByPath(current => ({ ...current, [directoryPath]: requestRevision }))
    setErrors((current) => {
      const next = { ...current }
      delete next[directoryPath]
      return next
    })
    try {
      const listing = await apiJson<DirectoryListing>(`/api/directory?path=${encodeURIComponent(directoryPath)}`)
      if (revisionRef.current === requestRevision) {
        setEntriesByPath(current => ({ ...current, [directoryPath]: listing.entries }))
        setLoadedRevisionByPath(current => ({ ...current, [directoryPath]: requestRevision }))
      }
    }
    catch (cause) {
      if (revisionRef.current === requestRevision) {
        setErrors(current => ({
          ...current,
          [directoryPath]: {
            revision: requestRevision,
            message: cause instanceof Error ? cause.message : String(cause),
          },
        }))
      }
    }
    finally {
      setLoadingRevisionByPath((current) => {
        if (current[directoryPath] !== requestRevision)
          return current
        const next = { ...current }
        delete next[directoryPath]
        return next
      })
    }
  }, [])

  useEffect(() => {
    const ancestors = ancestorPaths(currentPath)
    setExpanded(current => new Set([...current, ...ancestors]))
  }, [currentPath])

  useEffect(() => {
    for (const directoryPath of expanded) {
      const error = errors[directoryPath]
      if (
        loadedRevisionByPath[directoryPath] !== revision
        && loadingRevisionByPath[directoryPath] !== revision
        && error?.revision !== revision
      ) {
        void load(directoryPath, revision)
      }
    }
  }, [errors, expanded, load, loadedRevisionByPath, loadingRevisionByPath, revision])

  const clearError = (directoryPath: string): void => {
    setErrors((current) => {
      if (current[directoryPath] === undefined)
        return current
      const next = { ...current }
      delete next[directoryPath]
      return next
    })
  }

  const toggle = (directoryPath: string): void => {
    const opening = !expanded.has(directoryPath)
    setExpanded((current) => {
      const next = new Set(current)
      next.has(directoryPath) ? next.delete(directoryPath) : next.add(directoryPath)
      return next
    })
    if (opening)
      clearError(directoryPath)
  }

  const retry = (directoryPath: string): void => {
    clearError(directoryPath)
  }

  return (
    <aside className="navigation-pane" aria-label="File navigation">
      <div className="navigation-heading">Explorer</div>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="navigation-scroll-viewport">
        <div className="w-full min-w-0 overflow-hidden px-2 pb-3">
          <TreeRow
            label={rootName}
            level={0}
            active={currentPath === '' && previewPath === undefined}
            expanded={expanded.has('')}
            loading={loadingRevisionByPath[''] === revision}
            root
            onToggle={() => toggle('')}
            onActivate={() => onNavigate('')}
          />
          {expanded.has('') && errors['']?.revision === revision && <TreeError level={1} message={errors[''].message} onRetry={() => retry('')} />}
          {expanded.has('') && (entriesByPath[''] ?? []).map(entry => (
            <TreeBranch
              key={entry.path}
              entry={entry}
              level={1}
              currentPath={currentPath}
              previewPath={previewPath}
              entriesByPath={entriesByPath}
              expanded={expanded}
              loadingRevisionByPath={loadingRevisionByPath}
              errors={errors}
              revision={revision}
              onToggle={toggle}
              onRetry={retry}
              onNavigate={onNavigate}
              onOpenFile={onOpenFile}
            />
          ))}
        </div>
      </ScrollArea>
    </aside>
  )
}

function TreeBranch({
  entry,
  level,
  currentPath,
  previewPath,
  entriesByPath,
  expanded,
  loadingRevisionByPath,
  errors,
  revision,
  onToggle,
  onRetry,
  onNavigate,
  onOpenFile,
}: {
  entry: DirectoryEntry
  level: number
  currentPath: string
  previewPath?: string
  entriesByPath: Record<string, DirectoryEntry[] | undefined>
  expanded: Set<string>
  loadingRevisionByPath: Record<string, number | undefined>
  errors: Record<string, { revision: number, message: string } | undefined>
  revision: number
  onToggle: (path: string) => void
  onRetry: (path: string) => void
  onNavigate: (path: string) => void
  onOpenFile: (entry: DirectoryEntry) => void
}): React.JSX.Element {
  const directory = entry.kind === 'directory'
  const open = directory && expanded.has(entry.path)
  const active = directory ? currentPath === entry.path && previewPath === undefined : previewPath === entry.path
  const error = errors[entry.path]?.revision === revision ? errors[entry.path] : undefined
  return (
    <>
      <TreeRow
        entry={entry}
        label={entry.name}
        level={level}
        active={active}
        expanded={open}
        loading={directory && loadingRevisionByPath[entry.path] === revision}
        onToggle={directory ? () => onToggle(entry.path) : undefined}
        onActivate={entry.kind === 'directory' ? () => onNavigate(entry.path) : entry.kind === 'file' ? () => onOpenFile(entry) : undefined}
      />
      {open && error && <TreeError level={level + 1} message={error.message} onRetry={() => onRetry(entry.path)} />}
      {open && (entriesByPath[entry.path] ?? []).map(child => (
        <TreeBranch
          key={child.path}
          entry={child}
          level={level + 1}
          currentPath={currentPath}
          previewPath={previewPath}
          entriesByPath={entriesByPath}
          expanded={expanded}
          loadingRevisionByPath={loadingRevisionByPath}
          errors={errors}
          revision={revision}
          onToggle={onToggle}
          onRetry={onRetry}
          onNavigate={onNavigate}
          onOpenFile={onOpenFile}
        />
      ))}
    </>
  )
}

function TreeRow({
  entry,
  label,
  level,
  active,
  expanded,
  loading,
  root = false,
  onToggle,
  onActivate,
}: {
  entry?: DirectoryEntry
  label: string
  level: number
  active: boolean
  expanded: boolean
  loading: boolean
  root?: boolean
  onToggle?: () => void
  onActivate?: () => void
}): React.JSX.Element {
  const unavailable = entry?.kind === 'unavailable'
  return (
    <div className={cn('tree-row', active && 'active', unavailable && 'unavailable')} style={{ paddingLeft: `${level * 16 + 4}px` }}>
      {onToggle
        ? (
            <button type="button" aria-label={`${expanded ? 'Collapse' : 'Expand'} ${label}`} className="tree-chevron" onClick={onToggle}>
              {loading ? <LoaderCircle className="animate-spin" /> : <ChevronRight className={cn('transition-transform', expanded && 'rotate-90')} />}
            </button>
          )
        : <span className="tree-chevron tree-chevron-placeholder" aria-hidden="true" />}
      <button
        type="button"
        className="tree-label"
        title={unavailable ? `${label} is unavailable` : label}
        aria-current={active ? 'page' : undefined}
        disabled={onActivate === undefined}
        onClick={onActivate}
      >
        {root ? <HardDrive className="size-4 text-accent" /> : entry && <EntryGlyph entry={entry} className="size-4" />}
        <span className="truncate">{label}</span>
        {entry?.isSymlink && <LinkIcon aria-label="Symbolic link" className="ml-auto size-3 shrink-0 text-muted-foreground" />}
      </button>
    </div>
  )
}

function TreeError({ level, message, onRetry }: { level: number, message: string, onRetry: () => void }): React.JSX.Element {
  return (
    <button type="button" className="tree-error" style={{ paddingLeft: `${level * 16 + 24}px` }} title={message} onClick={onRetry}>
      <CircleAlert />
      <span className="truncate">Unable to load</span>
      <RefreshCw className="ml-auto" />
    </button>
  )
}
