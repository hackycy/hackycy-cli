import type { ComparisonStatus, Entry, FileNode, ServerState } from './api'
import { WorkerPoolContextProvider } from '@pierre/diffs/react'
import diffWorkerUrl from '@pierre/diffs/worker/worker-portable.js?url'
import { AlertTriangle, GitCompareArrows, RefreshCw } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Group, Panel, Separator, useDefaultLayout } from 'react-resizable-panels'
import { ScrollArea } from '../shared/components/ui/scroll-area'
import { Sheet, SheetContent } from '../shared/components/ui/sheet'
import { cn } from '../shared/lib/utils'
import { apiJson } from './api'
import { DiffPanel } from './components/diff-panel'
import { DiffToolbar } from './components/diff-toolbar'
import { EditorTabs } from './components/editor-tabs'
import { Sidebar } from './components/sidebar'
import { contentCache } from './lib/content-cache'

function createDiffWorker(): Worker {
  return new Worker(diffWorkerUrl, { type: 'module' })
}

const statusOptions: Array<{ status: ComparisonStatus, letter: string, label: string }> = [
  { status: 'added', letter: 'A', label: 'Added' },
  { status: 'deleted', letter: 'D', label: 'Deleted' },
  { status: 'modified', letter: 'M', label: 'Modified' },
  { status: 'unchanged', letter: 'U', label: 'Unchanged' },
  { status: 'issue', letter: '!', label: 'Issues' },
]

function useStoredValue<T>(key: string, fallback: T, allowedValues: readonly T[]): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const stored = localStorage.getItem(key)
      const parsed: unknown = stored === null ? undefined : JSON.parse(stored)
      return allowedValues.find(allowed => allowed === parsed) ?? fallback
    }
    catch {
      return fallback
    }
  })
  const update = (next: T): void => {
    setValue(next)
    try {
      localStorage.setItem(key, JSON.stringify(next))
    }
    catch {}
  }
  return [value, update]
}

const layoutStorage = {
  getItem(key: string): string | null {
    try {
      const stored = localStorage.getItem(key)
      const parsed: unknown = stored === null ? undefined : JSON.parse(stored)
      return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed) ? stored : null
    }
    catch {
      return null
    }
  },
  setItem(key: string, value: string): void {
    try {
      localStorage.setItem(key, value)
    }
    catch {}
  },
}

export function App(): React.JSX.Element {
  const [state, setState] = useState<ServerState>()
  const [search, setSearch] = useState('')
  const [statuses, setStatuses] = useState<Set<ComparisonStatus>>(new Set(['added', 'deleted', 'modified', 'unchanged', 'issue']))
  const [openTabs, setOpenTabs] = useState<Entry[]>([])
  const [activeEntryId, setActiveEntryId] = useState<number>()
  const [mobileSidebar, setMobileSidebar] = useState(false)
  const [diffStyle, setDiffStyle] = useStoredValue<'split' | 'unified'>('ycy-diff-style', 'split', ['split', 'unified'])
  const [wrap, setWrap] = useStoredValue('ycy-diff-wrap', false, [false, true])
  const [ignoreWhitespace, setIgnoreWhitespace] = useStoredValue('ycy-diff-whitespace', false, [false, true])
  const [theme, setTheme] = useStoredValue<'light' | 'dark'>('ycy-diff-theme', 'light', ['light', 'dark'])
  const [mobile, setMobile] = useState(matchMedia('(max-width: 899px)').matches)
  const savedDesktopLayout = useDefaultLayout({
    id: 'ycy-diff-layout',
    panelIds: ['sidebar', 'feed'],
    onlySaveAfterUserInteractions: true,
    storage: layoutStorage,
  })
  const snapshotId = state?.snapshot?.id
  const activeEntry = openTabs.find(entry => entry.id === activeEntryId)
  const refreshing = ['discovering', 'comparing', 'publishing'].includes(state?.workspace.phase ?? '')

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
  }, [theme])

  useEffect(() => {
    contentCache.clear()
    setOpenTabs([])
    setActiveEntryId(undefined)
  }, [snapshotId])

  useEffect(() => {
    const query = matchMedia('(max-width: 899px)')
    const change = (): void => setMobile(query.matches)
    query.addEventListener('change', change)
    return () => query.removeEventListener('change', change)
  }, [])

  useEffect(() => {
    void apiJson<ServerState>('/api/state').then(setState)
    const events = new EventSource('/api/events')
    events.onmessage = event => setState(JSON.parse(event.data) as ServerState)
    return () => events.close()
  }, [])

  const toggleStatus = (status: ComparisonStatus): void => {
    setStatuses((current) => {
      const next = new Set(current)
      next.has(status) ? next.delete(status) : next.add(status)
      return next
    })
  }

  const selectEntry = (entry: FileNode): void => {
    setMobileSidebar(false)
    setOpenTabs(current => current.some(tab => tab.id === entry.id) ? current : [...current, entry])
    setActiveEntryId(entry.id)
  }

  const closeEntry = (entry: Entry): void => {
    setOpenTabs((current) => {
      const closingIndex = current.findIndex(tab => tab.id === entry.id)
      const next = current.filter(tab => tab.id !== entry.id)
      if (activeEntryId === entry.id)
        setActiveEntryId(next[Math.min(closingIndex, next.length - 1)]?.id)
      return next
    })
  }

  const closeOtherEntries = (entry: Entry): void => {
    setOpenTabs(current => current.filter(tab => tab.id === entry.id))
    setActiveEntryId(entry.id)
  }

  const closeEntriesToRight = (entry: Entry): void => {
    setOpenTabs((current) => {
      const index = current.findIndex(tab => tab.id === entry.id)
      const next = index === -1 ? current : current.slice(0, index + 1)
      if (activeEntryId !== undefined && !next.some(tab => tab.id === activeEntryId))
        setActiveEntryId(entry.id)
      return next
    })
  }

  const closeAllEntries = (): void => {
    setOpenTabs([])
    setActiveEntryId(undefined)
  }

  const refresh = async (): Promise<void> => {
    await apiJson('/api/refresh', { method: 'POST' })
  }

  const cancelRefresh = async (): Promise<void> => {
    await fetch('/api/refresh', { method: 'DELETE' })
  }

  const effectiveStyle = mobile ? 'unified' : diffStyle
  const sidebar = snapshotId
    ? (
        <Sidebar
          key={snapshotId}
          snapshotId={snapshotId}
          search={search}
          statuses={statuses}
          activeId={activeEntryId}
          onSearch={setSearch}
          onSelect={selectEntry}
        />
      )
    : <div className="h-full bg-sidebar" />

  const roots = useMemo(() => ({
    baseline: state?.snapshot?.baselineDirectory ?? 'Baseline',
    target: state?.snapshot?.targetDirectory ?? 'Target',
  }), [state?.snapshot])
  const rootName = (value: string): string => value.split(/[\\/]/).at(-1) ?? value
  const editor = (
    <main className="flex h-full min-h-0 flex-col bg-feed">
      <EditorTabs tabs={openTabs} activeId={activeEntryId} onSelect={entry => setActiveEntryId(entry.id)} onClose={closeEntry} onCloseOthers={closeOtherEntries} onCloseToRight={closeEntriesToRight} onCloseAll={closeAllEntries} />
      <ScrollArea className="min-h-0 flex-1 bg-background" scrollbars="both">
        {!snapshotId && (
          <EmptyState
            title={state?.workspace.phase === 'canceled' ? 'Comparison canceled' : state?.workspace.error ?? 'Indexing directories'}
            busy={!state?.workspace.error && state?.workspace.phase !== 'canceled'}
          />
        )}
        {snapshotId && activeEntry && (
          <DiffPanel key={`${snapshotId}:${activeEntry.id}`} entry={activeEntry} snapshotId={snapshotId} diffStyle={effectiveStyle} wrap={wrap} ignoreWhitespace={ignoreWhitespace} theme={theme} />
        )}
        {snapshotId && !activeEntry && <EmptyState title="Select a file to compare" />}
      </ScrollArea>
    </main>
  )

  return (
    <WorkerPoolContextProvider poolOptions={{ poolSize: Math.min(navigator.hardwareConcurrency || 2, 4), workerFactory: createDiffWorker }} highlighterOptions={{}}>
      <div data-diff-style={effectiveStyle} className="flex h-dvh min-w-0 flex-col bg-background text-foreground">
        <header className="z-30 flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-background px-3 py-2">
          <div className="flex min-w-0 grow basis-80 items-center gap-2 font-semibold">
            <GitCompareArrows className="size-4 shrink-0 text-cyan-600" />
            <span className="shrink-0 text-sm">HACKYCY CLI — DIFF SERVER</span>
            <span className="hidden text-border sm:inline">/</span>
            <span className="hidden min-w-0 items-center gap-1 text-xs font-normal sm:flex">
              <span className="max-w-40 truncate" title={roots.baseline}>{rootName(roots.baseline)}</span>
              <span className="text-muted-foreground">→</span>
              <span className="max-w-40 truncate" title={roots.target}>{rootName(roots.target)}</span>
            </span>
          </div>
          <DiffToolbar
            refreshing={refreshing}
            diffStyle={effectiveStyle}
            wrap={wrap}
            ignoreWhitespace={ignoreWhitespace}
            theme={theme}
            onFiles={() => setMobileSidebar(true)}
            onRefresh={() => void (refreshing ? cancelRefresh() : refresh())}
            onDiffStyle={setDiffStyle}
            onWrap={() => setWrap(!wrap)}
            onIgnoreWhitespace={() => setIgnoreWhitespace(!ignoreWhitespace)}
            onTheme={() => setTheme(theme === 'light' ? 'dark' : 'light')}
          />
        </header>

        <div className="summary-bar z-20 flex h-10 shrink-0 items-center gap-1.5 overflow-x-auto border-b border-border bg-muted/35 px-3">
          {statusOptions.map(option => (
            <button key={option.status} type="button" aria-pressed={statuses.has(option.status)} className={cn('summary-filter', `summary-${option.status}`, statuses.has(option.status) && 'active')} onClick={() => toggleStatus(option.status)}>
              <span className="font-semibold">{option.letter}</span>
              <span>{option.label}</span>
              <span className="tabular-nums">{option.status === 'issue' ? state?.snapshot?.issues ?? 0 : state?.snapshot?.counts[option.status] ?? 0}</span>
            </button>
          ))}
          <span className="ml-auto flex items-center gap-1.5 whitespace-nowrap text-xs text-muted-foreground">
            {state?.workspace.phase === 'canceled' && <AlertTriangle className="size-3.5 text-amber-500" />}
            {workspaceProgressLabel(state)}
          </span>
        </div>

        <div className="min-h-0 flex-1">
          {mobile
            ? editor
            : (
                <Group
                  className="h-full"
                  orientation="horizontal"
                  defaultLayout={savedDesktopLayout.defaultLayout ?? { sidebar: 26, feed: 74 }}
                  onLayoutChange={savedDesktopLayout.onLayoutChange}
                  onLayoutChanged={savedDesktopLayout.onLayoutChanged}
                >
                  <Panel id="sidebar" minSize="280px" maxSize="420px">{sidebar}</Panel>
                  <Separator className="w-px bg-border outline-none hover:bg-cyan-500 focus-visible:bg-cyan-500" />
                  <Panel id="feed" minSize="55%">{editor}</Panel>
                </Group>
              )}
        </div>
      </div>
      <Sheet open={mobileSidebar} onOpenChange={setMobileSidebar}><SheetContent title="Files" description="Comparison file tree">{sidebar}</SheetContent></Sheet>
    </WorkerPoolContextProvider>
  )
}

function EmptyState({ title, busy = false }: { title: string, busy?: boolean }): React.JSX.Element {
  return (
    <div className="flex h-full min-h-64 items-center justify-center gap-2 text-sm text-muted-foreground">
      {busy && <RefreshCw className="size-4 animate-spin" />}
      <span>{title}</span>
    </div>
  )
}

function formatProgressBytes(value: number): string {
  if (value < 1024 * 1024)
    return `${Math.round(value / 1024)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

function workspaceProgressLabel(state: ServerState | undefined): string {
  const phase = state?.workspace.phase
  const progress = state?.workspace.progress
  const totalFiles = state?.snapshot
    ? Object.values(state.snapshot.counts).reduce((total, count) => total + count, 0) + state.snapshot.issues
    : 0
  if (phase === 'ready')
    return `${totalFiles} files`
  if (phase === 'canceled')
    return state?.snapshot ? `Refresh canceled · ${totalFiles} files` : 'Comparison canceled'
  if (phase === 'discovering')
    return `${progress?.discoveredEntries ?? 0} entries discovered`
  if (phase === 'comparing') {
    const entries = `${progress?.comparedEntries ?? 0}/${progress?.totalEntries ?? 0} entries`
    return progress?.totalBytes
      ? `${entries} · ${formatProgressBytes(progress.comparedBytes)}/${formatProgressBytes(progress.totalBytes)}`
      : entries
  }
  if (phase === 'publishing')
    return 'Publishing snapshot'
  if (phase === 'idle')
    return 'Starting'
  return phase ?? 'Starting'
}
