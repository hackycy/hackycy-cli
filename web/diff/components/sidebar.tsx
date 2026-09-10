import type { ComparisonStatus, FileNode, SearchPage, TreeNode } from '../api'
import { useVirtualizer } from '@tanstack/react-virtual'
import { AlertTriangle, ChevronRight, FileText, Folder, FolderOpen, Link as LinkIcon, LoaderCircle, Search } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Input } from '../../shared/components/ui/input'
import { ScrollArea } from '../../shared/components/ui/scroll-area'
import { cn } from '../../shared/lib/utils'
import { apiJson } from '../api'

interface VisibleNode {
  node: TreeNode
  depth: number
}

const statusClass = {
  added: 'bg-green-500',
  deleted: 'bg-red-500',
  modified: 'bg-amber-500',
  unchanged: 'bg-zinc-400',
  issue: 'bg-cyan-500',
}

export function Sidebar({
  snapshotId,
  search,
  statuses,
  activeId,
  onSearch,
  onSelect,
}: {
  snapshotId: string
  search: string
  statuses: Set<ComparisonStatus>
  activeId?: number
  onSearch: (value: string) => void
  onSelect: (entry: FileNode) => void
}): React.JSX.Element {
  const [children, setChildren] = useState<Map<string, TreeNode[]>>(new Map())
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [searchPage, setSearchPage] = useState<SearchPage>()
  const [searching, setSearching] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  const loadDirectory = useCallback(async (directory: string) => {
    const response = await apiJson<{ children: TreeNode[] }>(`/api/tree?snapshot=${encodeURIComponent(snapshotId)}&path=${encodeURIComponent(directory)}`)
    setChildren(current => new Map(current).set(directory, response.children))
  }, [snapshotId])

  useEffect(() => {
    setChildren(new Map())
    setExpanded(new Set())
    void loadDirectory('')
  }, [loadDirectory])

  useEffect(() => {
    const queryText = search.trim()
    if (!queryText) {
      setSearchPage(undefined)
      setSearching(false)
      return
    }
    const controller = new AbortController()
    setSearching(true)
    setSearchPage(undefined)
    const timer = setTimeout(() => {
      const query = new URLSearchParams({ snapshot: snapshotId, q: queryText, limit: '200' })
      for (const status of statuses)
        query.append('status', status)
      void apiJson<SearchPage>(`/api/search?${query}`, { signal: controller.signal })
        .then(setSearchPage)
        .catch(() => {
          if (!controller.signal.aborted)
            setSearchPage({ results: [], truncated: false })
        })
        .finally(() => {
          if (!controller.signal.aborted)
            setSearching(false)
        })
    }, 120)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [search, snapshotId, statuses])

  const matchesStatuses = useCallback((node: TreeNode): boolean => {
    if (node.kind !== 'directory')
      return statuses.has(node.status)
    return [...statuses].some(status => status === 'issue' ? node.issues > 0 : node.counts[status] > 0)
  }, [statuses])

  const visible = useMemo(() => {
    const result: VisibleNode[] = []
    const append = (directory: string, depth: number): void => {
      for (const node of children.get(directory) ?? []) {
        if (!matchesStatuses(node))
          continue
        result.push({ node, depth })
        if (node.kind === 'directory' && expanded.has(node.path))
          append(node.path, depth + 1)
      }
    }
    append('', 0)
    return result
  }, [children, expanded, matchesStatuses])

  const searchVisible = useMemo<VisibleNode[]>(() => (searchPage?.results ?? []).map(node => ({ node, depth: 0 })), [searchPage])
  const displayedNodes = search.trim() ? searchVisible : visible

  const virtualizer = useVirtualizer({
    count: displayedNodes.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 30,
    overscan: 10,
  })

  const toggleDirectory = async (directory: string): Promise<void> => {
    const opening = !expanded.has(directory)
    setExpanded((current) => {
      const next = new Set(current)
      opening ? next.add(directory) : next.delete(directory)
      return next
    })
    if (opening && !children.has(directory))
      await loadDirectory(directory)
  }

  const revealDirectory = async (directory: string): Promise<void> => {
    const parts = directory.split('/')
    const directories = parts.map((_, index) => parts.slice(0, index + 1).join('/'))
    await Promise.all(['', ...directories].filter(value => !children.has(value)).map(loadDirectory))
    setExpanded(current => new Set([...current, ...directories]))
    onSearch('')
  }

  return (
    <aside className="flex h-full min-h-0 flex-col bg-sidebar">
      <div className="border-b border-border p-3 pr-11 lg:pr-3">
        <div className="relative">
          {searching
            ? <LoaderCircle className="pointer-events-none absolute left-2.5 top-2 size-4 animate-spin text-muted-foreground" />
            : <Search className="pointer-events-none absolute left-2.5 top-2 size-4 text-muted-foreground" />}
          <Input value={search} onChange={event => onSearch(event.target.value)} placeholder="Search files or folders" className="pl-8" aria-label="Search files or folders" />
        </div>
      </div>
      <ScrollArea viewportRef={scrollRef} className="min-h-0 flex-1" viewportClassName="py-1">
        {!searching && search.trim() && displayedNodes.length === 0 && (
          <div className="px-3 py-6 text-center text-xs text-muted-foreground">No matching files or folders</div>
        )}
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const visibleNode = displayedNodes[item.index]!
            const { node, depth } = visibleNode
            const directory = node.kind === 'directory'
            const open = directory && expanded.has(node.path)
            const Icon = directory ? (open ? FolderOpen : Folder) : node.kind === 'issue' ? AlertTriangle : node.kind === 'symlink' ? LinkIcon : FileText
            return (
              <button
                key={node.path}
                type="button"
                className={cn(
                  'absolute left-0 top-0 flex h-[30px] w-full items-center gap-1.5 overflow-hidden px-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500',
                  !directory && node.id === activeId && 'bg-muted text-foreground',
                )}
                style={{ transform: `translateY(${item.start}px)`, paddingLeft: 8 + depth * 16 }}
                onClick={() => directory ? void (search.trim() ? revealDirectory(node.path) : toggleDirectory(node.path)) : onSelect(node)}
                title={node.path}
                aria-current={!directory && node.id === activeId ? 'page' : undefined}
              >
                <ChevronRight className={cn('size-3 shrink-0 transition-transform', (!directory || search.trim()) && 'invisible', open && 'rotate-90')} />
                <Icon className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate">{search.trim() ? node.path : node.name}</span>
                {directory
                  ? <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">{node.counts.added + node.counts.deleted + node.counts.modified + node.counts.unchanged + node.issues}</span>
                  : <span className={cn('size-1.5 shrink-0 rounded-full', statusClass[node.status])} />}
              </button>
            )
          })}
        </div>
        {searchPage?.truncated && <div className="px-3 py-2 text-center text-[11px] text-muted-foreground">Showing the first 200 matches</div>}
      </ScrollArea>
    </aside>
  )
}
