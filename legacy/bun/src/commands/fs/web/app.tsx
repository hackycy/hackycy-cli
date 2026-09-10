import type { FormEvent } from 'react'
import type { Layout, LayoutChangedMeta } from 'react-resizable-panels'
import type { DirectoryEntry, DirectoryListing, DownloadList, DownloadTask, ExtractionList, ExtractionTask, OperationCommand, OperationResult, SessionState } from './api'
import type { TextEditorTarget } from './components/text-editor-dialog'
import type { ExplorerClipboard, ExplorerSelection, NavigationHistory } from './explorer-state'
import type { ActivityTask, SortDirection, SortKey, ViewMode } from './types'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import {
  ArchiveRestore,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  ArrowUpDown,
  ChevronRight,
  ClipboardPaste,
  Copy,
  Download,
  FolderOpen,
  FolderPlus,
  Grid2X2,
  HardDrive,
  List,
  ListChecks,
  LoaderCircle,
  LogIn,
  LogOut,
  Menu,
  Moon,
  MoreHorizontal,
  PanelRight,
  Pencil,
  RefreshCw,
  Scissors,
  Search,
  Sun,
  Trash2,
  Upload,
  UserRound,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Group, Panel, Separator, usePanelRef } from 'react-resizable-panels'
import { v4 as uuidv4 } from 'uuid'
import { Button } from '../../../shared/web/components/ui/button'
import { Sheet, SheetContent } from '../../../shared/web/components/ui/sheet'
import { Tooltip } from '../../../shared/web/components/ui/tooltip'
import { cn } from '../../../shared/web/lib/utils'
import { apiJson, applyOperation, cancelDownload, cancelExtraction, clearDownloads as clearServerDownloads, clearExtractions as clearServerExtractions, createDownload, createExtractions, retryDownload, retryExtraction, uploadChunkedFile, uploadFile } from './api'
import { ActivityCenter } from './components/activity-center'
import { DeleteDialog } from './components/delete-dialog'
import { DownloadDialog } from './components/download-dialog'
import { FileBrowser } from './components/file-browser'
import { NavigationPane } from './components/navigation-pane'
import { PreviewPane, PreviewSheet } from './components/preview-sheet'
import { RenameDialog } from './components/rename-dialog'
import { TextEditorDialog } from './components/text-editor-dialog'
import {
  clipboardOperation,
  createNavigationHistory,
  entryNameError,
  extractableSelection,
  mergeExtractionTasks,
  moveNavigation,
  operationActivities,
  parentDirectoryPath,
  pushNavigation,
  selectEntry,
  settleClipboard,
  visibleEntries,
} from './explorer-state'
import {
  NAVIGATION_PANEL_MAX_WIDTH,
  NAVIGATION_PANEL_MIN_WIDTH,
  NAVIGATION_PANEL_WIDTH_STORAGE_KEY,
  navigationPanelWidth,
} from './layout-state'

interface RouteState {
  directoryPath: string
  previewPath?: string
  error?: string
}

function routeState(): RouteState {
  const pathname = window.location.pathname
  const encodedPath = pathname === '/' || pathname === '/browse'
    ? ''
    : pathname.startsWith('/browse/') ? pathname.slice('/browse/'.length) : ''
  try {
    return {
      directoryPath: encodedPath.split('/').filter(Boolean).map(decodeURIComponent).join('/'),
      previewPath: new URLSearchParams(window.location.search).get('preview') ?? undefined,
    }
  }
  catch {
    return { directoryPath: '', error: 'The browser path is not valid URL encoding.' }
  }
}

function browserUrl(relativePath: string): string {
  return relativePath ? `/browse/${relativePath.split('/').map(encodeURIComponent).join('/')}` : '/'
}

function useStoredValue<T>(key: string, fallback: () => T): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const stored = localStorage.getItem(key)
      return stored === null ? fallback() : JSON.parse(stored) as T
    }
    catch {
      return fallback()
    }
  })
  const update = (next: T): void => {
    setValue(next)
    localStorage.setItem(key, JSON.stringify(next))
  }
  return [value, update]
}

function useMobile(): boolean {
  const [mobile, setMobile] = useState(() => matchMedia('(max-width: 899px)').matches)
  useEffect(() => {
    const query = matchMedia('(max-width: 899px)')
    const change = (): void => setMobile(query.matches)
    query.addEventListener('change', change)
    return () => query.removeEventListener('change', change)
  }, [])
  return mobile
}

function Login({ onLogin }: { onLogin: (session: Extract<SessionState, { authenticated: true }>) => void }): React.JSX.Element {
  const [error, setError] = useState<string>()
  const [submitting, setSubmitting] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setError(undefined)
    setSubmitting(true)
    try {
      const session = await apiJson<SessionState>('/api/session', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: form.get('username'), password: form.get('password') }),
      })
      if (!session.authenticationEnabled || !session.authenticated)
        throw new Error('The server did not create a session')
      onLogin(session)
    }
    catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="auth-shell">
      <div className="brand-line" />
      <main className="auth-stage">
        <form className="login-panel" onSubmit={event => void submit(event)}>
          <div className="login-brand">
            <span className="app-icon"><HardDrive className="size-4" /></span>
            <div>
              <div className="brand-label">HACKYCY CLI</div>
              <strong>File Browser</strong>
            </div>
          </div>
          <label className="login-field">
            <span>Username</span>
            <input name="username" autoComplete="username" maxLength={64} required autoFocus />
          </label>
          <label className="login-field">
            <span>Password</span>
            <input name="password" type="password" autoComplete="current-password" minLength={5} maxLength={256} required />
          </label>
          {error && <p role="alert" className="login-error">{error}</p>}
          <Button className="login-submit" type="submit" disabled={submitting}>
            {submitting ? <LoaderCircle className="size-4 animate-spin" /> : <LogIn className="size-4" />}
            {submitting ? 'Signing in' : 'Sign in'}
          </Button>
        </form>
      </main>
    </div>
  )
}

export function App(): React.JSX.Element {
  const [theme, setTheme] = useStoredValue<'light' | 'dark'>('ycy-fs-theme', () => matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
  const [session, setSession] = useState<SessionState>()
  const [sessionError, setSessionError] = useState<string>()
  const loadSession = useCallback(async (): Promise<void> => {
    setSessionError(undefined)
    try {
      setSession(await apiJson<SessionState>('/api/session'))
    }
    catch (cause) {
      setSessionError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [])

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
  }, [theme])

  useEffect(() => void loadSession(), [loadSession])

  useEffect(() => {
    const sessionEnded = (): void => setSession({ version: 1, authenticationEnabled: true, authenticated: false })
    window.addEventListener('fs-authentication-required', sessionEnded)
    return () => window.removeEventListener('fs-authentication-required', sessionEnded)
  }, [])

  useEffect(() => {
    if (!session?.authenticationEnabled || !session.authenticated)
      return
    const refresh = (): void => {
      void apiJson<SessionState>('/api/session').then(setSession).catch(() => {})
    }
    const interval = window.setInterval(refresh, 24 * 60 * 60 * 1000)
    return () => window.clearInterval(interval)
  }, [session])

  if (!session) {
    return (
      <div className="auth-shell">
        <div className="brand-line" />
        <main className="auth-stage">
          {sessionError
            ? (
                <div className="session-state" role="alert">
                  <span>{sessionError}</span>
                  <Button type="button" variant="outline" onClick={() => void loadSession()}>Retry</Button>
                </div>
              )
            : (
                <div className="session-state">
                  <LoaderCircle className="size-5 animate-spin" />
                  <span>Loading</span>
                </div>
              )}
        </main>
      </div>
    )
  }

  if (session.authenticationEnabled && !session.authenticated)
    return <Login onLogin={setSession} />

  const account = session.authenticationEnabled ? session.account : undefined
  const signOut = async (): Promise<void> => {
    try {
      await apiJson<void>('/api/session', { method: 'DELETE' })
    }
    finally {
      setSession({ version: 1, authenticationEnabled: true, authenticated: false })
    }
  }
  return <ExplorerApp account={account} theme={theme} setTheme={setTheme} onSignOut={() => void signOut()} />
}

function ExplorerApp({
  account,
  theme,
  setTheme,
  onSignOut,
}: {
  account?: { username: string }
  theme: 'light' | 'dark'
  setTheme: (theme: 'light' | 'dark') => void
  onSignOut: () => void
}): React.JSX.Element {
  const initialRoute = useMemo(routeState, [])
  const [initialNavigationPanelWidth] = useState(() => {
    try {
      return navigationPanelWidth(localStorage.getItem(NAVIGATION_PANEL_WIDTH_STORAGE_KEY))
    }
    catch {
      return navigationPanelWidth(null)
    }
  })
  const [directoryPath, setDirectoryPath] = useState(initialRoute.directoryPath)
  const [previewPath, setPreviewPath] = useState(initialRoute.previewPath)
  const [routeError, setRouteError] = useState(initialRoute.error)
  const [navigation, setNavigation] = useState<NavigationHistory>(() => createNavigationHistory(initialRoute.directoryPath))
  const navigationRef = useRef(navigation)
  const [listing, setListing] = useState<DirectoryListing>()
  const [loadError, setLoadError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [reloadVersion, setReloadVersion] = useState(0)
  const [viewMode, setViewMode] = useStoredValue<ViewMode>('ycy-fs-view', () => 'list')
  const [sortKey, setSortKey] = useStoredValue<SortKey>('ycy-fs-sort', () => 'name')
  const [sortDirection, setSortDirection] = useStoredValue<SortDirection>('ycy-fs-sort-direction', () => 'asc')
  const [query, setQuery] = useState('')
  const [selection, setSelection] = useState<ExplorerSelection>({ paths: [] })
  const [clipboard, setClipboard] = useState<ExplorerClipboard>()
  const [creatingFolder, setCreatingFolder] = useState(false)
  const [renamingPath, setRenamingPath] = useState<string>()
  const [editingBusy, setEditingBusy] = useState(false)
  const [editingError, setEditingError] = useState<string>()
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [operationBusy, setOperationBusy] = useState(false)
  const [activities, setActivities] = useState<ActivityTask[]>([])
  const [downloadTasks, setDownloadTasks] = useState<DownloadTask[]>([])
  const [extractionTasks, setExtractionTasks] = useState<ExtractionTask[]>([])
  const [downloadDialogOpen, setDownloadDialogOpen] = useState(false)
  const [downloadBusy, setDownloadBusy] = useState(false)
  const [downloadError, setDownloadError] = useState<string>()
  const [uploadActive, setUploadActive] = useState(false)
  const [dragging, setDragging] = useState(false)
  const [mobileNavigation, setMobileNavigation] = useState(false)
  const [imageViewerOpen, setImageViewerOpen] = useState(false)
  const [editingTarget, setEditingTarget] = useState<TextEditorTarget>()
  const [editorDirty, setEditorDirty] = useState(false)
  const [toast, setToast] = useState<string>()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadControllersRef = useRef(new Map<string, AbortController>())
  const currentRouteRef = useRef({ directoryPath, previewPath })
  const editorDirtyRef = useRef(editorDirty)
  const revertingPopStateRef = useRef(false)
  const activeResizeHandleRef = useRef<'navigation' | 'preview' | undefined>(undefined)
  const navigationPanelRef = usePanelRef()
  const mobile = useMobile()

  useEffect(() => {
    const cancelUploads = (): void => {
      for (const controller of uploadControllersRef.current.values())
        controller.abort()
    }
    window.addEventListener('pagehide', cancelUploads)
    return () => {
      window.removeEventListener('pagehide', cancelUploads)
      cancelUploads()
    }
  }, [])

  const saveNavigationPanelWidth = useCallback((_layout: Layout, meta: LayoutChangedMeta): void => {
    const activeHandle = activeResizeHandleRef.current
    activeResizeHandleRef.current = undefined
    if (!meta.isUserInteraction || activeHandle !== 'navigation')
      return
    const width = navigationPanelRef.current?.getSize().inPixels
    if (width === undefined)
      return
    try {
      localStorage.setItem(NAVIGATION_PANEL_WIDTH_STORAGE_KEY, JSON.stringify(Math.round(width)))
    }
    catch {}
  }, [navigationPanelRef])

  useEffect(() => {
    navigationRef.current = navigation
  }, [navigation])

  useEffect(() => {
    currentRouteRef.current = { directoryPath, previewPath }
  }, [directoryPath, previewPath])

  useEffect(() => {
    editorDirtyRef.current = editorDirty
  }, [editorDirty])

  useEffect(() => {
    history.replaceState({ fsCursor: 0 }, '', window.location.href)
    const popstate = (event: PopStateEvent): void => {
      const next = routeState()
      const cursor = typeof event.state?.fsCursor === 'number' ? event.state.fsCursor : undefined
      if (revertingPopStateRef.current) {
        revertingPopStateRef.current = false
      }
      else if (editorDirtyRef.current) {
        // eslint-disable-next-line no-alert
        if (!window.confirm('Discard unsaved changes?')) {
          const current = currentRouteRef.current
          if (cursor !== undefined && cursor !== navigationRef.current.cursor) {
            revertingPopStateRef.current = true
            history.go(navigationRef.current.cursor - cursor)
          }
          else {
            const restoreUrl = new URL(browserUrl(current.directoryPath), window.location.origin)
            if (current.previewPath)
              restoreUrl.searchParams.set('preview', current.previewPath)
            history.replaceState({ fsCursor: navigationRef.current.cursor }, '', restoreUrl)
          }
          return
        }
      }
      if (cursor !== undefined && navigationRef.current.paths[cursor] === next.directoryPath)
        setNavigation(current => ({ ...current, cursor }))
      else
        setNavigation(createNavigationHistory(next.directoryPath))
      setDirectoryPath(next.directoryPath)
      setPreviewPath(next.previewPath)
      setEditingTarget(undefined)
      setEditorDirty(false)
      setImageViewerOpen(false)
      setRouteError(next.error)
      setQuery('')
      setSelection({ paths: [] })
      setCreatingFolder(false)
      setRenamingPath(undefined)
      setEditingError(undefined)
    }
    window.addEventListener('popstate', popstate)
    return () => window.removeEventListener('popstate', popstate)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setLoadError(undefined)
    void apiJson<DirectoryListing>(`/api/directory?path=${encodeURIComponent(directoryPath)}`, { signal: controller.signal })
      .then((next) => {
        setListing(next)
        setRouteError(undefined)
        const available = new Set(next.entries.map(entry => entry.path))
        setSelection(current => ({
          paths: current.paths.filter(path => available.has(path)),
          anchorPath: current.anchorPath && available.has(current.anchorPath) ? current.anchorPath : undefined,
        }))
      })
      .catch((cause) => {
        if (!controller.signal.aborted)
          setLoadError(cause instanceof Error ? cause.message : String(cause))
      })
      .finally(() => {
        if (!controller.signal.aborted)
          setLoading(false)
      })
    return () => controller.abort()
  }, [directoryPath, reloadVersion])

  useEffect(() => {
    if (!listing?.managementEnabled) {
      setDownloadTasks([])
      return
    }
    let active = true
    let events: EventSource | undefined
    const applyDownloadTasks = (body: DownloadList): void => {
      setDownloadTasks((current) => {
        const previous = new Map(current.map(task => [task.id, task]))
        if (body.tasks.some(task => task.status === 'done' && previous.get(task.id)?.status !== 'done' && task.directoryPath === directoryPath))
          setReloadVersion(version => version + 1)
        return body.tasks
      })
    }
    void apiJson<DownloadList>('/api/downloads')
      .then((body) => {
        if (!active)
          return
        applyDownloadTasks(body)
        events = new EventSource('/api/downloads/events')
        events.onmessage = (event) => {
          if (!active)
            return
          try {
            applyDownloadTasks(JSON.parse(event.data) as DownloadList)
          }
          catch {
            setDownloadError('Download progress update was invalid')
          }
        }
        events.onerror = () => {
          void apiJson<SessionState>('/api/session').then((session) => {
            if (session.authenticationEnabled && !session.authenticated)
              window.dispatchEvent(new Event('fs-authentication-required'))
          }).catch(() => {})
        }
      })
      .catch((cause) => {
        if (active)
          setDownloadError(cause instanceof Error ? cause.message : String(cause))
      })
    return () => {
      active = false
      events?.close()
    }
  }, [directoryPath, listing?.managementEnabled])

  useEffect(() => {
    if (!listing?.managementEnabled) {
      setExtractionTasks([])
      return
    }
    let active = true
    let events: EventSource | undefined
    const applyExtractionTasks = (body: ExtractionList): void => {
      setExtractionTasks((current) => {
        const previous = new Map(current.map(task => [task.id, task]))
        if (body.tasks.some(task => task.status === 'done' && previous.get(task.id)?.status !== 'done' && parentDirectoryPath(task.archivePath) === directoryPath))
          setReloadVersion(version => version + 1)
        return body.tasks
      })
    }
    void apiJson<ExtractionList>('/api/extractions')
      .then((body) => {
        if (!active)
          return
        applyExtractionTasks(body)
        events = new EventSource('/api/extractions/events')
        events.onmessage = (event) => {
          if (!active)
            return
          try {
            applyExtractionTasks(JSON.parse(event.data) as ExtractionList)
          }
          catch {
            setToast('Extraction progress update was invalid')
          }
        }
        events.onerror = () => {
          void apiJson<SessionState>('/api/session').then((session) => {
            if (session.authenticationEnabled && !session.authenticated)
              window.dispatchEvent(new Event('fs-authentication-required'))
          }).catch(() => {})
        }
      })
      .catch((cause) => {
        if (active)
          setToast(cause instanceof Error ? cause.message : String(cause))
      })
    return () => {
      active = false
      events?.close()
    }
  }, [directoryPath, listing?.managementEnabled])

  useEffect(() => {
    if (!toast)
      return
    const timeout = window.setTimeout(() => setToast(undefined), 4000)
    return () => window.clearTimeout(timeout)
  }, [toast])

  const navigate = useCallback((path: string): void => {
    if (path === directoryPath) {
      setMobileNavigation(false)
      return
    }
    // eslint-disable-next-line no-alert
    if (editorDirty && !window.confirm('Discard unsaved changes?'))
      return
    const next = pushNavigation(navigationRef.current, path)
    history.pushState({ fsCursor: next.cursor }, '', browserUrl(path))
    setNavigation(next)
    setDirectoryPath(path)
    setPreviewPath(undefined)
    setEditingTarget(undefined)
    setEditorDirty(false)
    setImageViewerOpen(false)
    setRouteError(undefined)
    setQuery('')
    setSelection({ paths: [] })
    setCreatingFolder(false)
    setRenamingPath(undefined)
    setEditingError(undefined)
    setMobileNavigation(false)
  }, [directoryPath, editorDirty])

  const moveHistory = (offset: -1 | 1): void => {
    // eslint-disable-next-line no-alert
    if (editorDirty && !window.confirm('Discard unsaved changes?'))
      return
    const next = moveNavigation(navigationRef.current, offset)
    if (next.cursor === navigationRef.current.cursor)
      return
    offset === -1 ? history.back() : history.forward()
  }

  const openPreview = useCallback((entry: DirectoryEntry): void => {
    if (entry.kind !== 'file')
      return
    // eslint-disable-next-line no-alert
    if (entry.path !== previewPath && editorDirty && !window.confirm('Discard unsaved changes?'))
      return
    const url = new URL(window.location.href)
    url.searchParams.set('preview', entry.path)
    history.replaceState({ fsCursor: navigationRef.current.cursor }, '', url)
    setPreviewPath(entry.path)
    if (entry.path !== previewPath) {
      setEditingTarget(undefined)
      setEditorDirty(false)
    }
    setImageViewerOpen(false)
    setSelection({ paths: [entry.path], anchorPath: entry.path })
  }, [editorDirty, previewPath])

  const openTreeFile = useCallback((entry: DirectoryEntry): void => {
    if (entry.kind !== 'file')
      return
    setMobileNavigation(false)
    const parentPath = parentDirectoryPath(entry.path)
    if (parentPath === directoryPath) {
      openPreview(entry)
      return
    }
    // eslint-disable-next-line no-alert
    if (editorDirty && !window.confirm('Discard unsaved changes?'))
      return

    const next = pushNavigation(navigationRef.current, parentPath)
    const previewUrl = new URL(browserUrl(parentPath), window.location.origin)
    previewUrl.searchParams.set('preview', entry.path)
    history.pushState({ fsCursor: next.cursor }, '', previewUrl)
    setNavigation(next)
    setDirectoryPath(parentPath)
    setPreviewPath(entry.path)
    setEditingTarget(undefined)
    setEditorDirty(false)
    setImageViewerOpen(false)
    setRouteError(undefined)
    setQuery('')
    setSelection({ paths: [entry.path], anchorPath: entry.path })
    setCreatingFolder(false)
    setRenamingPath(undefined)
    setEditingError(undefined)
  }, [directoryPath, openPreview, editorDirty, previewPath])

  const closePreview = useCallback((confirmed = false): void => {
    // eslint-disable-next-line no-alert
    if (editorDirty && !confirmed && !window.confirm('Discard unsaved changes?'))
      return
    const url = new URL(window.location.href)
    url.searchParams.delete('preview')
    history.replaceState({ fsCursor: navigationRef.current.cursor }, '', url)
    setPreviewPath(undefined)
    setEditingTarget(undefined)
    setEditorDirty(false)
    setImageViewerOpen(false)
  }, [editorDirty])

  const changeSort = (key: SortKey): void => {
    if (key === sortKey) {
      setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc')
      return
    }
    setSortKey(key)
    setSortDirection('asc')
  }

  const chooseSort = (key: SortKey): void => {
    if (key === sortKey)
      return
    setSortKey(key)
    setSortDirection('asc')
  }

  const updateActivity = (id: string, update: Partial<ActivityTask>): void => {
    setActivities(current => current.map(task => task.id === id ? { ...task, ...update } : task))
  }

  const upsertDownloadTask = (task: DownloadTask): void => {
    setDownloadTasks(current => current.some(item => item.id === task.id)
      ? current.map(item => item.id === task.id ? task : item)
      : [task, ...current])
  }

  const submitDownload = async (url: string, filename: string): Promise<void> => {
    setDownloadBusy(true)
    setDownloadError(undefined)
    try {
      const response = await createDownload(url, directoryPath, filename || undefined)
      upsertDownloadTask(response.task)
      setDownloadDialogOpen(false)
    }
    catch (cause) {
      setDownloadError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setDownloadBusy(false)
    }
  }

  const stopDownload = async (id: string): Promise<void> => {
    try {
      const response = await cancelDownload(id)
      upsertDownloadTask(response.task)
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const retryRemoteDownload = async (id: string): Promise<void> => {
    try {
      const response = await retryDownload(id)
      upsertDownloadTask(response.task)
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const clearRemoteDownloads = async (): Promise<void> => {
    if (downloadTasks.some(task => task.status === 'queued' || task.status === 'running'))
      return
    try {
      await clearServerDownloads()
      setDownloadTasks([])
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const upsertExtractionTasks = (nextTasks: ExtractionTask[]): void => {
    setExtractionTasks(current => mergeExtractionTasks(current, nextTasks))
  }

  const stopExtraction = async (id: string): Promise<void> => {
    try {
      const response = await cancelExtraction(id)
      upsertExtractionTasks([response.task])
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const retryArchiveExtraction = async (id: string): Promise<void> => {
    try {
      const response = await retryExtraction(id)
      upsertExtractionTasks([response.task])
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const clearArchiveExtractions = async (): Promise<void> => {
    if (extractionTasks.some(task => task.status === 'queued' || task.status === 'running'))
      return
    try {
      await clearServerExtractions()
      setExtractionTasks([])
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const runOperation = useCallback(async (label: string, command: OperationCommand): Promise<OperationResult | undefined> => {
    const id = uuidv4()
    setActivities(current => [{ id, label, status: 'running', detail: 'Working' }, ...current])
    setOperationBusy(true)
    try {
      const result = await applyOperation(command)
      const errors = result.items.filter(item => item.status === 'error')
      setActivities(current => current.flatMap(task => task.id === id ? operationActivities(id, label, result) : [task]))
      if (errors.length > 0)
        setToast(errors[0]!.error.message)
      setReloadVersion(version => version + 1)
      return result
    }
    catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause)
      updateActivity(id, { status: 'error', detail: message })
      setToast(message)
      return undefined
    }
    finally {
      setOperationBusy(false)
    }
  }, [])

  const entries = useMemo(
    () => visibleEntries(listing?.entries ?? [], query, sortKey, sortDirection),
    [listing?.entries, query, sortDirection, sortKey],
  )

  const selectAll = (): void => {
    const paths = entries.map(entry => entry.path)
    setSelection({ paths, anchorPath: paths.at(-1) })
  }
  const entryMap = useMemo(() => new Map((listing?.entries ?? []).map(entry => [entry.path, entry])), [listing?.entries])
  const selectedSet = useMemo(() => new Set(selection.paths), [selection.paths])
  const selectedEntries = selection.paths.map(path => entryMap.get(path)).filter((entry): entry is DirectoryEntry => entry !== undefined)
  const previewEntry = entryMap.get(previewPath ?? '')
  const renamingEntry = entryMap.get(renamingPath ?? '')
  const managementEnabled = listing?.managementEnabled ?? false
  const canExtractSelection = extractableSelection(listing?.entries ?? [], selection.paths)
  const visibleError = routeError ?? loadError

  const selectedPathsFor = (entry?: DirectoryEntry): string[] => entry && !selectedSet.has(entry.path) ? [entry.path] : selection.paths

  const extractSelection = async (entry?: DirectoryEntry): Promise<void> => {
    const paths = entry
      ? canExtractSelection && selectedSet.has(entry.path) ? selection.paths : [entry.path]
      : canExtractSelection ? selection.paths : []
    if (!managementEnabled || paths.length === 0)
      return
    try {
      const response = await createExtractions(paths)
      upsertExtractionTasks(response.tasks)
    }
    catch (cause) {
      setToast(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const startCreateFolder = (): void => {
    if (!managementEnabled || operationBusy)
      return
    setCreatingFolder(true)
    setRenamingPath(undefined)
    setEditingError(undefined)
    setSelection({ paths: [] })
  }

  const createFolder = async (name: string): Promise<void> => {
    const error = entryNameError(name)
    if (error) {
      setEditingError(error)
      return
    }
    setEditingBusy(true)
    const result = await runOperation(`Create ${name}`, { action: 'create-directory', parentPath: directoryPath, name })
    const item = result?.items[0]
    if (item?.status === 'ok') {
      setCreatingFolder(false)
      setEditingError(undefined)
      if (item.destinationPath)
        setSelection({ paths: [item.destinationPath], anchorPath: item.destinationPath })
    }
    else if (item?.status === 'error') {
      setEditingError(item.error.message)
    }
    setEditingBusy(false)
  }

  const startRename = (entry: DirectoryEntry): void => {
    if (!managementEnabled || operationBusy)
      return
    setSelection({ paths: [entry.path], anchorPath: entry.path })
    setCreatingFolder(false)
    setRenamingPath(entry.path)
    setEditingError(undefined)
  }

  const renameEntry = async (name: string): Promise<void> => {
    const entry = renamingEntry
    if (!entry)
      return
    const error = entryNameError(name)
    if (error) {
      setEditingError(error)
      return
    }
    if (name === entry.name) {
      setRenamingPath(undefined)
      return
    }
    setEditingBusy(true)
    const result = await runOperation(`Rename ${entry.name}`, { action: 'rename', path: entry.path, newName: name })
    const item = result?.items[0]
    if (item?.status === 'ok') {
      setRenamingPath(undefined)
      setEditingError(undefined)
      if (item.destinationPath)
        setSelection({ paths: [item.destinationPath], anchorPath: item.destinationPath })
      if (previewPath === entry.path)
        closePreview()
    }
    else if (item?.status === 'error') {
      setEditingError(item.error.message)
    }
    setEditingBusy(false)
  }

  const copySelection = (mode: 'copy' | 'move', entry?: DirectoryEntry): void => {
    const paths = selectedPathsFor(entry)
    if (managementEnabled && paths.length > 0)
      setClipboard({ mode, paths })
  }

  const pasteClipboard = async (destinationPath = directoryPath): Promise<void> => {
    if (!clipboard || operationBusy)
      return
    const result = await runOperation(clipboard.mode === 'copy' ? 'Copy items' : 'Move items', clipboardOperation(clipboard, destinationPath))
    if (!result)
      return
    setClipboard(settleClipboard(clipboard, result.items))
    const destinations = result.items.filter(item => item.status === 'ok' && item.destinationPath).map(item => item.destinationPath!)
    if (destinationPath === directoryPath && destinations.length > 0)
      setSelection({ paths: destinations, anchorPath: destinations.at(-1) })
  }

  const requestDelete = (entry?: DirectoryEntry): void => {
    const paths = selectedPathsFor(entry)
    if (managementEnabled && paths.length > 0) {
      setSelection({ paths, anchorPath: paths.at(-1) })
      setDeleteOpen(true)
    }
  }

  const confirmDelete = async (): Promise<void> => {
    const paths = selection.paths
    const result = await runOperation(`Delete ${paths.length} item${paths.length === 1 ? '' : 's'}`, { action: 'delete', paths })
    if (result) {
      const deleted = new Set(result.items.filter(item => item.status === 'ok').map(item => item.sourcePath))
      if (previewPath && deleted.has(previewPath))
        closePreview()
      setSelection(current => ({ paths: current.paths.filter(path => !deleted.has(path)) }))
      setDeleteOpen(false)
    }
  }

  const openEntry = useCallback((entry: DirectoryEntry): void => {
    if (entry.kind === 'directory')
      navigate(entry.path)
    else if (entry.kind === 'file')
      openPreview(entry)
  }, [navigate, openPreview])

  const selectFile = (entry: DirectoryEntry, modifiers: { toggle: boolean, range: boolean }): void => {
    setSelection(current => selectEntry(entries.map(item => item.path), current, entry.path, modifiers))
  }

  const enqueueUploads = useCallback(async (files: File[]) => {
    if (files.length === 0 || uploadActive || !listing?.managementEnabled)
      return
    const canChunk = (file: File): boolean => listing.chunkedUpload !== undefined && file.size > listing.chunkedUpload.thresholdBytes
    const tooLarge = (file: File): boolean => file.size > listing.maxUploadBytes && !canChunk(file)
    const tasks = files.map(file => ({
      id: uuidv4(),
      file,
      activity: {
        id: uuidv4(),
        label: file.name,
        status: tooLarge(file) ? 'error' as const : 'queued' as const,
        progress: 0,
        detail: tooLarge(file) ? 'File exceeds the 1 GiB direct-upload limit' : 'Waiting to upload',
      },
    }))
    setActivities(current => [...tasks.map(task => task.activity), ...current])
    setUploadActive(true)
    const pending = tasks.filter(task => task.activity.status === 'queued')
    let cursor = 0
    const worker = async (): Promise<void> => {
      while (cursor < pending.length) {
        const item = pending[cursor++]!
        const controller = new AbortController()
        uploadControllersRef.current.set(item.activity.id, controller)
        updateActivity(item.activity.id, { cancel: () => controller.abort() })
        updateActivity(item.activity.id, { status: 'running', progress: 0, detail: 'Uploading' })
        try {
          const result = canChunk(item.file)
            ? await uploadChunkedFile(directoryPath, item.file, listing.chunkedUpload!, progress => updateActivity(item.activity.id, { progress }), controller.signal, detail => updateActivity(item.activity.id, { detail }))
            : await uploadFile(directoryPath, item.file, progress => updateActivity(item.activity.id, { progress }), controller.signal)
          updateActivity(item.activity.id, { status: 'done', progress: 100, detail: result.filename === item.file.name ? 'Uploaded' : `Saved as ${result.filename}` })
        }
        catch (cause) {
          const cancelled = cause instanceof DOMException && cause.name === 'AbortError'
          updateActivity(item.activity.id, { status: cancelled ? 'cancelled' : 'error', detail: cancelled ? 'Cancelled' : cause instanceof Error ? cause.message : String(cause) })
        }
        finally {
          uploadControllersRef.current.delete(item.activity.id)
          updateActivity(item.activity.id, { cancel: undefined })
        }
      }
    }
    await Promise.all(Array.from({ length: Math.min(3, pending.length) }, () => worker()))
    setUploadActive(false)
    setReloadVersion(version => version + 1)
  }, [directoryPath, listing, uploadActive])

  useEffect(() => {
    if (!listing?.managementEnabled)
      return
    let dragDepth = 0
    let internalDrag = false
    const start = (): void => {
      internalDrag = true
      dragDepth = 0
      setDragging(false)
    }
    const end = (): void => {
      internalDrag = false
      dragDepth = 0
      setDragging(false)
    }
    const enter = (event: DragEvent): void => {
      if (internalDrag || !event.dataTransfer?.types.includes('Files'))
        return
      event.preventDefault()
      dragDepth++
      setDragging(true)
    }
    const leave = (event: DragEvent): void => {
      event.preventDefault()
      dragDepth = Math.max(0, dragDepth - 1)
      if (dragDepth === 0)
        setDragging(false)
    }
    const over = (event: DragEvent): void => {
      if (!internalDrag && event.dataTransfer?.types.includes('Files'))
        event.preventDefault()
    }
    const drop = (event: DragEvent): void => {
      if (internalDrag) {
        end()
        return
      }
      if (!event.dataTransfer?.types.includes('Files'))
        return
      event.preventDefault()
      dragDepth = 0
      setDragging(false)
      void enqueueUploads(Array.from(event.dataTransfer?.files ?? []))
    }
    document.addEventListener('dragstart', start)
    document.addEventListener('dragend', end)
    document.addEventListener('dragenter', enter)
    document.addEventListener('dragleave', leave)
    document.addEventListener('dragover', over)
    document.addEventListener('drop', drop)
    return () => {
      document.removeEventListener('dragstart', start)
      document.removeEventListener('dragend', end)
      document.removeEventListener('dragenter', enter)
      document.removeEventListener('dragleave', leave)
      document.removeEventListener('dragover', over)
      document.removeEventListener('drop', drop)
    }
  }, [enqueueUploads, listing?.managementEnabled])

  const refresh = (): void => setReloadVersion(version => version + 1)
  const cancelEdit = (): void => {
    setCreatingFolder(false)
    setRenamingPath(undefined)
    setEditingError(undefined)
  }

  const fileArea = (
    <main className="flex h-full min-h-0 flex-1 flex-col bg-content">
      {loading && !listing && <CenteredState icon={<LoaderCircle className="size-5 animate-spin" />} label="Loading folder" />}
      {!loading && visibleError && !listing && <CenteredState icon={<FolderOpen className="size-6" />} label="Folder unavailable" />}
      {listing && (
        <FileBrowser
          entries={entries}
          viewMode={viewMode}
          sortKey={sortKey}
          direction={sortDirection}
          selectedPaths={selectedSet}
          managementEnabled={managementEnabled}
          canPaste={clipboard !== undefined}
          creatingFolder={creatingFolder}
          editingBusy={editingBusy}
          editingError={editingError}
          emptyLabel={entries.length === 0 && !creatingFolder && !loading ? (query ? 'No items match your search' : 'This folder is empty') : undefined}
          onSort={changeSort}
          onSelect={selectFile}
          onOpen={openEntry}
          onCreateFolder={name => void createFolder(name)}
          onCancelEdit={cancelEdit}
          onCut={entry => copySelection('move', entry)}
          onCopy={entry => copySelection('copy', entry)}
          onPaste={destination => void pasteClipboard(destination)}
          onStartRename={startRename}
          onDelete={requestDelete}
          onExtract={entry => void extractSelection(entry)}
          onNewFolder={startCreateFolder}
          onRefresh={refresh}
        />
      )}
    </main>
  )

  const navigationPane = (
    <NavigationPane
      rootName={listing?.rootName ?? 'Root'}
      currentPath={directoryPath}
      previewPath={previewPath}
      revision={reloadVersion}
      onNavigate={navigate}
      onOpenFile={openTreeFile}
    />
  )

  return (
    <div className="explorer-shell">
      <div className="brand-line" />
      <header className="title-bar">
        <Button className="mobile-nav-trigger command-button" size="icon" variant="ghost" aria-label="Open folder navigation" onClick={() => setMobileNavigation(true)}><Menu className="size-4" /></Button>
        <span className="app-icon"><HardDrive className="size-4" /></span>
        <div className="min-w-0">
          <div className="brand-label">HACKYCY CLI · FILE BROWSER</div>
          <div className="truncate text-xs font-semibold" title={listing?.rootName}>{listing?.rootName ?? 'File Browser'}</div>
        </div>
        <span className={cn('mode-badge', managementEnabled && 'manage')}>{managementEnabled ? 'MANAGEMENT' : 'READ ONLY'}</span>
        <Tooltip label={`Use ${theme === 'light' ? 'dark' : 'light'} theme`}>
          <Button aria-label={`Use ${theme === 'light' ? 'dark' : 'light'} theme`} className="ml-auto command-button" size="icon" variant="ghost" onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}>
            {theme === 'light' ? <Moon className="size-4" /> : <Sun className="size-4" />}
          </Button>
        </Tooltip>
        {account && (
          <div className="account-identity" title={account.username}>
            <UserRound className="size-3.5" />
            <span>{account.username}</span>
          </div>
        )}
        {account && (
          <Tooltip label="Sign out">
            <Button aria-label="Sign out" className="command-button" size="icon" variant="ghost" onClick={onSignOut}>
              <LogOut className="size-4" />
            </Button>
          </Tooltip>
        )}
      </header>

      <div className="address-bar">
        <div className="navigation-buttons">
          <Tooltip label="Back"><Button className="command-button" size="icon" variant="ghost" aria-label="Back" disabled={navigation.cursor === 0} onClick={() => moveHistory(-1)}><ArrowLeft className="size-4" /></Button></Tooltip>
          <Tooltip label="Forward"><Button className="command-button" size="icon" variant="ghost" aria-label="Forward" disabled={navigation.cursor >= navigation.paths.length - 1} onClick={() => moveHistory(1)}><ArrowRight className="size-4" /></Button></Tooltip>
          <Tooltip label="Up"><Button className="command-button" size="icon" variant="ghost" aria-label="Up" disabled={!listing?.parentPath && directoryPath === ''} onClick={() => navigate(listing?.parentPath ?? '')}><ArrowUp className="size-4" /></Button></Tooltip>
        </div>
        <Breadcrumb path={listing?.path ?? directoryPath} rootName={listing?.rootName ?? 'Root'} onNavigate={navigate} />
        <label className="search-box">
          <Search className="size-3.5 shrink-0 text-muted-foreground" />
          <input
            aria-label="Search current folder"
            value={query}
            placeholder={`Search ${directoryPath.split('/').at(-1) || listing?.rootName || 'folder'}`}
            onChange={(event) => {
              setQuery(event.target.value)
              setSelection({ paths: [] })
            }}
          />
          {query && <button type="button" aria-label="Clear search" onClick={() => setQuery('')}><X className="size-3.5" /></button>}
        </label>
      </div>

      <div role="toolbar" aria-label="File commands" className="command-bar">
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(event) => {
            void enqueueUploads(Array.from(event.currentTarget.files ?? []))
            event.currentTarget.value = ''
          }}
        />
        {managementEnabled && <IconCommand icon={<FolderPlus />} label="New folder" disabled={operationBusy} onClick={startCreateFolder} />}
        {managementEnabled && <IconCommand icon={<Upload />} label="Upload" disabled={uploadActive} onClick={() => fileInputRef.current?.click()} />}
        {managementEnabled && (
          <IconCommand
            icon={<Download />}
            label="Download from URL"
            onClick={() => {
              setDownloadError(undefined)
              setDownloadDialogOpen(true)
            }}
          />
        )}
        {managementEnabled && <span className="command-separator desktop-command" />}
        {managementEnabled && (
          <div className="desktop-command flex items-center gap-0.5">
            <IconCommand icon={<Scissors />} label="Cut" disabled={selection.paths.length === 0} onClick={() => copySelection('move')} />
            <IconCommand icon={<Copy />} label="Copy" disabled={selection.paths.length === 0} onClick={() => copySelection('copy')} />
            <IconCommand icon={<ClipboardPaste />} label="Paste" disabled={!clipboard || operationBusy} onClick={() => void pasteClipboard()} />
            <IconCommand icon={<ArchiveRestore />} label="Extract" disabled={!canExtractSelection} onClick={() => void extractSelection()} />
            <IconCommand icon={<Pencil />} label="Rename" disabled={selectedEntries.length !== 1 || operationBusy} onClick={() => selectedEntries[0] && startRename(selectedEntries[0])} />
            <IconCommand icon={<Trash2 />} label="Delete" destructive disabled={selection.paths.length === 0 || operationBusy} onClick={() => requestDelete()} />
          </div>
        )}
        {managementEnabled && <span className="command-separator desktop-command" />}
        <div className="view-switch desktop-command">
          <IconCommand icon={<List />} label="Details view" pressed={viewMode === 'list'} onClick={() => setViewMode('list')} />
          <IconCommand icon={<Grid2X2 />} label="Grid view" pressed={viewMode === 'grid'} onClick={() => setViewMode('grid')} />
        </div>
        <IconCommand className="desktop-command" icon={<PanelRight />} label="Preview selected file" disabled={selectedEntries.length !== 1 || selectedEntries[0]?.kind !== 'file'} pressed={previewEntry !== undefined} onClick={() => selectedEntries[0] && openPreview(selectedEntries[0])} />
        <IconCommand className="desktop-command" icon={<RefreshCw className={loading ? 'animate-spin' : ''} />} label="Refresh" disabled={loading} onClick={refresh} />
        <MoreMenu
          mobile={mobile}
          managementEnabled={managementEnabled}
          hasSelection={selection.paths.length > 0}
          oneSelected={selectedEntries.length === 1}
          canSelectAll={entries.length > 0}
          canPaste={clipboard !== undefined}
          canExtract={canExtractSelection}
          sortKey={sortKey}
          direction={sortDirection}
          onCut={() => copySelection('move')}
          onCopy={() => copySelection('copy')}
          onPaste={() => void pasteClipboard()}
          onExtract={() => void extractSelection()}
          onRename={() => selectedEntries[0] && startRename(selectedEntries[0])}
          onDelete={() => requestDelete()}
          onSelectAll={selectAll}
          onList={() => setViewMode('list')}
          onGrid={() => setViewMode('grid')}
          onPreview={() => selectedEntries[0] && openPreview(selectedEntries[0])}
          onRefresh={refresh}
          onSort={chooseSort}
          onDirection={setSortDirection}
        />
      </div>

      {visibleError && <div role="alert" className="error-banner">{visibleError}</div>}

      <div className="min-h-0 flex-1">
        {mobile
          ? fileArea
          : (
              <Group orientation="horizontal" className="h-full" onLayoutChanged={saveNavigationPanelWidth}>
                <Panel
                  id="navigation"
                  panelRef={navigationPanelRef}
                  defaultSize={initialNavigationPanelWidth}
                  minSize={NAVIGATION_PANEL_MIN_WIDTH}
                  maxSize={NAVIGATION_PANEL_MAX_WIDTH}
                  groupResizeBehavior="preserve-pixel-size"
                >
                  {navigationPane}
                </Panel>
                <Separator
                  className="resize-handle"
                  onPointerDown={() => activeResizeHandleRef.current = 'navigation'}
                  onKeyDown={() => activeResizeHandleRef.current = 'navigation'}
                >
                  <span />
                </Separator>
                <Panel id="files" minSize="360px">{fileArea}</Panel>
                {previewEntry && (
                  <>
                    <Separator
                      className="resize-handle"
                      onPointerDown={() => activeResizeHandleRef.current = 'preview'}
                      onKeyDown={() => activeResizeHandleRef.current = 'preview'}
                    >
                      <span />
                    </Separator>
                    <Panel id="preview" defaultSize="360px" minSize="300px" maxSize="48%">
                      <PreviewPane entry={previewEntry} theme={theme} managementEnabled={managementEnabled} imageViewerOpen={imageViewerOpen} onImageViewerOpenChange={setImageViewerOpen} onClose={closePreview} onEdit={setEditingTarget} />
                    </Panel>
                  </>
                )}
              </Group>
            )}
      </div>

      <footer className="status-bar">
        <span>{`${listing?.entries.length ?? 0} ${(listing?.entries.length ?? 0) === 1 ? 'item' : 'items'}`}</span>
        {selection.paths.length > 0 && <span>{`${selection.paths.length} selected`}</span>}
        {query && <span>{`${entries.length} matches`}</span>}
        <span className="ml-auto truncate">{`/${listing?.path ?? directoryPath}`}</span>
      </footer>

      <Sheet open={mobileNavigation} onOpenChange={setMobileNavigation}>
        <SheetContent side="left" title="File navigation" description="Browse files and folders" className="mobile-sheet flex w-[min(88vw,340px)] flex-col pt-12">{navigationPane}</SheetContent>
      </Sheet>
      {mobile && <PreviewSheet entry={previewEntry} theme={theme} managementEnabled={managementEnabled} imageViewerOpen={imageViewerOpen} onImageViewerOpenChange={setImageViewerOpen} onClose={closePreview} onEdit={setEditingTarget} />}
      <TextEditorDialog target={editingTarget} theme={theme} onDirtyChange={setEditorDirty} onSaved={refresh} onClose={() => setEditingTarget(undefined)} />
      <RenameDialog
        entry={renamingEntry}
        busy={editingBusy}
        serverError={editingError}
        onOpenChange={open => !open && cancelEdit()}
        onNameChange={() => setEditingError(undefined)}
        onConfirm={name => void renameEntry(name)}
      />
      <DownloadDialog
        open={downloadDialogOpen}
        directoryPath={directoryPath}
        busy={downloadBusy}
        serverError={downloadError}
        onOpenChange={(open) => {
          setDownloadDialogOpen(open)
          if (!open)
            setDownloadError(undefined)
        }}
        onInputChange={() => setDownloadError(undefined)}
        onSubmit={(url, filename) => void submitDownload(url, filename)}
      />
      <DeleteDialog paths={selection.paths} open={deleteOpen} busy={operationBusy} onOpenChange={setDeleteOpen} onConfirm={() => void confirmDelete()} />
      <ActivityCenter
        tasks={activities}
        downloads={downloadTasks}
        extractions={extractionTasks}
        onClear={() => setActivities([])}
        onCancelDownload={id => void stopDownload(id)}
        onRetryDownload={id => void retryRemoteDownload(id)}
        onClearDownloads={() => void clearRemoteDownloads()}
        onCancelExtraction={id => void stopExtraction(id)}
        onRetryExtraction={id => void retryArchiveExtraction(id)}
        onClearExtractions={() => void clearArchiveExtractions()}
      />
      {toast && (
        <div role="status" className="toast">
          <span>{toast}</span>
          <button type="button" aria-label="Dismiss" onClick={() => setToast(undefined)}><X className="size-3.5" /></button>
        </div>
      )}
      {dragging && (
        <div className="drop-overlay">
          <Upload className="size-6" />
          <span>{`Drop files into /${listing?.path ?? ''}`}</span>
        </div>
      )}
    </div>
  )
}

function Breadcrumb({ path, rootName, onNavigate }: { path: string, rootName: string, onNavigate: (path: string) => void }): React.JSX.Element {
  const segments = path.split('/').filter(Boolean)
  return (
    <nav aria-label="Breadcrumb" className="breadcrumb-box">
      <button type="button" title={rootName} onClick={() => onNavigate('')}>
        <HardDrive className="size-3.5 text-accent" />
        <span className="truncate">{rootName}</span>
      </button>
      {segments.map((segment, index) => {
        const segmentPath = segments.slice(0, index + 1).join('/')
        return (
          <span key={segmentPath} className="breadcrumb-segment">
            <ChevronRight className="size-3 text-muted-foreground" />
            <button type="button" title={segment} onClick={() => onNavigate(segmentPath)}>{segment}</button>
          </span>
        )
      })}
    </nav>
  )
}

function IconCommand({ icon, label, disabled, destructive = false, pressed, className, onClick }: { icon: React.ReactElement, label: string, disabled?: boolean, destructive?: boolean, pressed?: boolean, className?: string, onClick: () => void }): React.JSX.Element {
  return (
    <Tooltip label={label}>
      <Button aria-label={label} aria-pressed={pressed} className={cn('command-button', destructive && 'destructive', pressed && 'active', className)} size="icon" variant="ghost" disabled={disabled} onClick={onClick}>{icon}</Button>
    </Tooltip>
  )
}

function MoreMenu(props: {
  mobile: boolean
  managementEnabled: boolean
  hasSelection: boolean
  oneSelected: boolean
  canSelectAll: boolean
  canPaste: boolean
  canExtract: boolean
  sortKey: SortKey
  direction: SortDirection
  onCut: () => void
  onCopy: () => void
  onPaste: () => void
  onExtract: () => void
  onRename: () => void
  onDelete: () => void
  onSelectAll: () => void
  onList: () => void
  onGrid: () => void
  onPreview: () => void
  onRefresh: () => void
  onSort: (key: SortKey) => void
  onDirection: (direction: SortDirection) => void
}): React.JSX.Element {
  return (
    <DropdownMenu.Root modal={false}>
      <Tooltip label="More commands">
        <DropdownMenu.Trigger asChild><Button className="command-more command-button ml-auto" size="icon" variant="ghost" aria-label="More commands"><MoreHorizontal /></Button></DropdownMenu.Trigger>
      </Tooltip>
      <DropdownMenu.Portal>
        <DropdownMenu.Content className="menu-content" align="end" sideOffset={4}>
          <DropdownMenu.Item className="menu-item" disabled={!props.canSelectAll} onSelect={props.onSelectAll}>
            <ListChecks />
            <span>Select all</span>
          </DropdownMenu.Item>
          <DropdownMenu.Separator className="menu-separator" />
          {props.mobile && props.managementEnabled && (
            <DropdownMenu.Item className="menu-item" disabled={!props.hasSelection} onSelect={props.onCut}>
              <Scissors />
              <span>Cut</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && props.managementEnabled && (
            <DropdownMenu.Item className="menu-item" disabled={!props.hasSelection} onSelect={props.onCopy}>
              <Copy />
              <span>Copy</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && props.managementEnabled && (
            <DropdownMenu.Item className="menu-item" disabled={!props.canPaste} onSelect={props.onPaste}>
              <ClipboardPaste />
              <span>Paste</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && props.managementEnabled && (
            <DropdownMenu.Item className="menu-item" disabled={!props.canExtract} onSelect={props.onExtract}>
              <ArchiveRestore />
              <span>Extract</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && props.managementEnabled && (
            <DropdownMenu.Item className="menu-item" disabled={!props.oneSelected} onSelect={props.onRename}>
              <Pencil />
              <span>Rename</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && props.managementEnabled && (
            <DropdownMenu.Item className="menu-item destructive" disabled={!props.hasSelection} onSelect={props.onDelete}>
              <Trash2 />
              <span>Delete</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && props.managementEnabled && <DropdownMenu.Separator className="menu-separator" />}
          {props.mobile && (
            <DropdownMenu.Item className="menu-item" onSelect={props.onList}>
              <List />
              <span>Details view</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && (
            <DropdownMenu.Item className="menu-item" onSelect={props.onGrid}>
              <Grid2X2 />
              <span>Grid view</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && (
            <DropdownMenu.Item className="menu-item" disabled={!props.oneSelected} onSelect={props.onPreview}>
              <PanelRight />
              <span>Preview</span>
            </DropdownMenu.Item>
          )}
          {props.mobile && <DropdownMenu.Separator className="menu-separator" />}
          <SortSubmenu sortKey={props.sortKey} direction={props.direction} onSort={props.onSort} onDirection={props.onDirection} />
          {props.mobile && <DropdownMenu.Separator className="menu-separator" />}
          {props.mobile && (
            <DropdownMenu.Item className="menu-item" onSelect={props.onRefresh}>
              <RefreshCw />
              <span>Refresh</span>
            </DropdownMenu.Item>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

function SortSubmenu({ sortKey, direction, onSort, onDirection }: {
  sortKey: SortKey
  direction: SortDirection
  onSort: (key: SortKey) => void
  onDirection: (direction: SortDirection) => void
}): React.JSX.Element {
  return (
    <DropdownMenu.Sub>
      <DropdownMenu.SubTrigger className="menu-item">
        <ArrowUpDown />
        <span>Sort by</span>
        <ChevronRight className="menu-sub-chevron" />
      </DropdownMenu.SubTrigger>
      <DropdownMenu.Portal>
        <DropdownMenu.SubContent className="menu-content" sideOffset={6} alignOffset={-4}>
          <DropdownItem label="Name" checked={sortKey === 'name'} onSelect={() => onSort('name')} />
          <DropdownItem label="Date modified" checked={sortKey === 'modified'} onSelect={() => onSort('modified')} />
          <DropdownItem label="Size" checked={sortKey === 'size'} onSelect={() => onSort('size')} />
          <DropdownMenu.Separator className="menu-separator" />
          <DropdownItem label="Ascending" checked={direction === 'asc'} onSelect={() => onDirection('asc')} />
          <DropdownItem label="Descending" checked={direction === 'desc'} onSelect={() => onDirection('desc')} />
        </DropdownMenu.SubContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Sub>
  )
}

function DropdownItem({ label, checked, onSelect }: { label: string, checked: boolean, onSelect: () => void }): React.JSX.Element {
  return (
    <DropdownMenu.Item className="menu-item" onSelect={onSelect}>
      <span className="menu-check">{checked ? '✓' : ''}</span>
      <span>{label}</span>
    </DropdownMenu.Item>
  )
}

function CenteredState({ icon, label }: { icon: React.ReactNode, label: string }): React.JSX.Element {
  return (
    <div className="centered-state">
      {icon}
      <span>{label}</span>
    </div>
  )
}
