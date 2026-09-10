import type { ComparisonStatus, Entry, FileNode, ServerState } from './api'
import { WorkerPoolContextProvider } from '@pierre/diffs/react'
import diffWorkerUrl from '@pierre/diffs/worker/worker-portable.js?url'
import { AlertTriangle, Columns2, GitCompareArrows, List, Menu, Moon, RefreshCw, Square, Sun, Text, WrapText } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Group, Panel, Separator, useDefaultLayout } from 'react-resizable-panels'
import { Button } from '../shared/components/ui/button'
import { ScrollArea } from '../shared/components/ui/scroll-area'
import { Sheet, SheetContent } from '../shared/components/ui/sheet'
import { Tooltip } from '../shared/components/ui/tooltip'
import { cn } from '../shared/lib/utils'
import { apiJson } from './api'
import { DiffPanel } from './components/diff-panel'
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

const toolbarButtonClass = 'size-8 text-muted-foreground hover:bg-muted/70 hover:text-foreground'
const toolbarActiveClass = 'bg-muted text-foreground'

function useStoredValue<T>(key: string, fallback: T): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => {
    const stored = localStorage.getItem(key)
    return stored ? JSON.parse(stored) as T : fallback
  })
  const update = (next: T): void => {
    setValue(next)
    localStorage.setItem(key, JSON.stringify(next))
  }
  return [value, update]
}

export function App(): React.JSX.Element {
  const [state, setState] = useState<ServerState>()
  const [search, setSearch] = useState('')
  const [statuses, setStatuses] = useState<Set<ComparisonStatus>>(new Set(['added', 'deleted', 'modified', 'unchanged', 'issue']))
  const [openTabs, setOpenTabs] = useState<Entry[]>([])
  const [activeEntryId, setActiveEntryId] = useState<number>()
  const [mobileSidebar, setMobileSidebar] = useState(false)
  const [diffStyle, setDiffStyle] = useStoredValue<'split' | 'unified'>('ycy-diff-style', 'split')
  const [wrap, setWrap] = useStoredValue('ycy-diff-wrap', false)
  const [ignoreWhitespace, setIgnoreWhitespace] = useStoredValue('ycy-diff-whitespace', false)
  const [theme, setTheme] = useStoredValue<'light' | 'dark'>('ycy-diff-theme', 'light')
  const [mobile, setMobile] = useState(matchMedia('(max-width: 899px)').matches)
  const savedDesktopLayout = useDefaultLayout({
    id: 'ycy-diff-layout',
    panelIds: ['sidebar', 'feed'],
    onlySaveAfterUserInteractions: true,
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
        <header className="z-30 flex h-12 shrink-0 items-center gap-2 border-b border-border bg-background px-3">
          <div className="flex min-w-0 items-center gap-2 font-semibold">
            <GitCompareArrows className="size-4 shrink-0 text-cyan-600" />
            <span className="shrink-0">HACKYCY CLI — DIFF SERVER</span>
            <span className="hidden text-border sm:inline">/</span>
            <span className="hidden min-w-0 items-center gap-1 text-xs font-normal sm:flex">
              <span className="max-w-40 truncate" title={roots.baseline}>{rootName(roots.baseline)}</span>
              <span className="text-muted-foreground">→</span>
              <span className="max-w-40 truncate" title={roots.target}>{rootName(roots.target)}</span>
            </span>
          </div>
          <div role="toolbar" aria-label="Diff controls" className="ml-auto flex items-center gap-0.5">
            <div className="flex items-center gap-0.5">
              <Tooltip label="Files"><Button aria-label="Files" className={cn(toolbarButtonClass, 'min-[900px]:hidden')} size="icon" variant="ghost" onClick={() => setMobileSidebar(true)}><Menu className="size-4" /></Button></Tooltip>
              <Tooltip label={refreshing ? 'Cancel comparison refresh' : 'Refresh comparison'}>
                <Button
                  aria-label={refreshing ? 'Cancel comparison refresh' : 'Refresh comparison'}
                  size="icon"
                  variant="ghost"
                  className={toolbarButtonClass}
                  onClick={() => void (refreshing ? cancelRefresh() : refresh())}
                >
                  {refreshing ? <Square className="size-3.5" /> : <RefreshCw className="size-4" />}
                </Button>
              </Tooltip>
            </div>
            <span aria-hidden="true" className="mx-1 hidden h-4 w-px bg-border/70 sm:block" />
            <div className="hidden items-center gap-0.5 sm:flex">
              <Tooltip label="Split diff"><Button aria-label="Split diff" aria-pressed={diffStyle === 'split'} size="icon" variant="ghost" className={cn(toolbarButtonClass, diffStyle === 'split' && toolbarActiveClass)} onClick={() => setDiffStyle('split')}><Columns2 className="size-3.5" /></Button></Tooltip>
              <Tooltip label="Unified diff"><Button aria-label="Unified diff" aria-pressed={diffStyle === 'unified'} size="icon" variant="ghost" className={cn(toolbarButtonClass, diffStyle === 'unified' && toolbarActiveClass)} onClick={() => setDiffStyle('unified')}><List className="size-3.5" /></Button></Tooltip>
            </div>
            <span aria-hidden="true" className="mx-1 hidden h-4 w-px bg-border/70 sm:block" />
            <div className="flex items-center gap-0.5">
              <Tooltip label={wrap ? 'Disable line wrapping' : 'Wrap long lines'}><Button aria-label={wrap ? 'Disable line wrapping' : 'Wrap long lines'} aria-pressed={wrap} size="icon" variant="ghost" className={cn(toolbarButtonClass, wrap && toolbarActiveClass)} onClick={() => setWrap(!wrap)}><WrapText className="size-4" /></Button></Tooltip>
              <Tooltip label={ignoreWhitespace ? 'Show exact diff' : 'Ignore whitespace'}><Button aria-label={ignoreWhitespace ? 'Show exact diff' : 'Ignore whitespace'} aria-pressed={ignoreWhitespace} size="icon" variant="ghost" className={cn(toolbarButtonClass, ignoreWhitespace && toolbarActiveClass)} onClick={() => setIgnoreWhitespace(!ignoreWhitespace)}><Text className="size-4" /></Button></Tooltip>
            </div>
            <span aria-hidden="true" className="mx-1 h-4 w-px bg-border/70" />
            <Tooltip label={`Use ${theme === 'light' ? 'dark' : 'light'} theme`}><Button aria-label={`Use ${theme === 'light' ? 'dark' : 'light'} theme`} aria-pressed={theme === 'dark'} size="icon" variant="ghost" className={cn(toolbarButtonClass, theme === 'dark' && toolbarActiveClass)} onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>{theme === 'light' ? <Moon className="size-4" /> : <Sun className="size-4" />}</Button></Tooltip>
          </div>
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
