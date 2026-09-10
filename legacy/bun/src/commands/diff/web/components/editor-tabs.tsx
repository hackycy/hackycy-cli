import type { Entry } from '../api'
import { CircleX, FileCode2, FileWarning, Link as LinkIcon, X } from 'lucide-react'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ScrollArea } from '../../../../shared/web/components/ui/scroll-area'
import { cn } from '../../../../shared/web/lib/utils'

const statusClass = {
  added: 'bg-green-500',
  deleted: 'bg-red-500',
  modified: 'bg-amber-500',
  unchanged: 'bg-zinc-400',
  issue: 'bg-cyan-500',
}

const statusLabel = {
  added: 'Added',
  deleted: 'Deleted',
  modified: 'Modified',
  unchanged: 'Unchanged',
  issue: 'Issue',
}

function fileName(filePath: string): string {
  return filePath.split('/').at(-1) ?? filePath
}

function tabLabel(entry: Entry, tabs: Entry[]): string {
  const name = fileName(entry.path)
  return tabs.some(other => other.id !== entry.id && fileName(other.path) === name) ? entry.path : name
}

function scrollTabsWithWheel(event: React.WheelEvent<HTMLDivElement>): void {
  const viewport = event.currentTarget
  if (viewport.scrollWidth <= viewport.clientWidth)
    return

  const distance = event.deltaX || event.deltaY
  if (!distance)
    return

  viewport.scrollLeft += distance
  event.preventDefault()
}

interface TabContextMenu {
  entry: Entry
  x: number
  y: number
}

export function EditorTabs({
  tabs,
  activeId,
  onSelect,
  onClose,
  onCloseOthers,
  onCloseToRight,
  onCloseAll,
}: {
  tabs: Entry[]
  activeId?: number
  onSelect: (entry: Entry) => void
  onClose: (entry: Entry) => void
  onCloseOthers: (entry: Entry) => void
  onCloseToRight: (entry: Entry) => void
  onCloseAll: () => void
}): React.JSX.Element {
  const viewportRef = useRef<HTMLDivElement>(null)
  const tabRefs = useRef(new Map<number, HTMLDivElement>())
  const contextMenuRef = useRef<HTMLDivElement>(null)
  const [contextMenu, setContextMenu] = useState<TabContextMenu>()

  useLayoutEffect(() => {
    if (activeId === undefined)
      return

    const viewport = viewportRef.current
    const tab = tabRefs.current.get(activeId)
    if (!viewport || !tab)
      return

    const viewportBounds = viewport.getBoundingClientRect()
    const tabBounds = tab.getBoundingClientRect()
    const offset = tabBounds.left < viewportBounds.left
      ? tabBounds.left - viewportBounds.left
      : tabBounds.right > viewportBounds.right
        ? tabBounds.right - viewportBounds.right
        : 0

    if (offset)
      viewport.scrollTo({ left: viewport.scrollLeft + offset, behavior: 'smooth' })
  }, [activeId])

  useEffect(() => {
    if (!contextMenu)
      return

    const dismiss = (event: PointerEvent): void => {
      if (!contextMenuRef.current?.contains(event.target as Node))
        setContextMenu(undefined)
    }
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key === 'Escape')
        setContextMenu(undefined)
    }
    document.addEventListener('pointerdown', dismiss)
    document.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('pointerdown', dismiss)
      document.removeEventListener('keydown', closeOnEscape)
    }
  }, [contextMenu])

  const openContextMenu = (event: React.MouseEvent<HTMLDivElement>, entry: Entry): void => {
    event.preventDefault()
    setContextMenu({
      entry,
      x: Math.max(8, Math.min(event.clientX, window.innerWidth - 200)),
      y: Math.max(8, Math.min(event.clientY, window.innerHeight - 148)),
    })
  }

  const runContextAction = (action: () => void): void => {
    action()
    setContextMenu(undefined)
  }

  const contextIndex = contextMenu ? tabs.findIndex(tab => tab.id === contextMenu.entry.id) : -1

  return (
    <>
      <ScrollArea
        className="h-10 shrink-0 border-b border-border bg-muted/30"
        scrollbars="none"
        viewportRef={viewportRef}
        viewportProps={{ 'role': 'tablist', 'aria-label': 'Open files', 'onWheel': scrollTabsWithWheel }}
      >
        <div className="flex h-10 min-w-max items-stretch">
          {tabs.length === 0 && <div className="flex items-center px-3 text-xs text-muted-foreground">No open files</div>}
          {tabs.map((entry) => {
            const active = entry.id === activeId
            const Icon = entry.status === 'issue' ? FileWarning : entry.kind === 'symlink' ? LinkIcon : FileCode2
            return (
              <div
                key={entry.id}
                ref={(element) => {
                  if (element)
                    tabRefs.current.set(entry.id, element)
                  else
                    tabRefs.current.delete(entry.id)
                }}
                className={cn(
                  'group flex min-w-36 max-w-56 shrink-0 items-center border-r border-t-2 border-border text-xs',
                  active ? 'border-t-cyan-600 bg-background text-foreground' : 'border-t-transparent text-muted-foreground hover:bg-muted',
                )}
                onContextMenu={event => openContextMenu(event, entry)}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={active}
                  className="flex h-full min-w-0 flex-1 items-center gap-1.5 px-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500"
                  aria-label={`Open ${entry.path}`}
                  onClick={() => onSelect(entry)}
                  title={entry.path}
                >
                  <Icon className="size-3.5 shrink-0" />
                  <span className="min-w-0 truncate font-mono">{tabLabel(entry, tabs)}</span>
                  <span className={cn('size-1.5 shrink-0 rounded-full', statusClass[entry.status])} title={statusLabel[entry.status]} />
                </button>
                <button
                  type="button"
                  className="mr-1 flex size-6 shrink-0 items-center justify-center rounded-sm opacity-60 outline-none hover:bg-muted hover:opacity-100 focus-visible:ring-2 focus-visible:ring-cyan-500"
                  aria-label={`Close ${entry.path}`}
                  onClick={() => onClose(entry)}
                >
                  <X className="size-3.5" />
                </button>
              </div>
            )
          })}
        </div>
      </ScrollArea>
      {contextMenu && (
        <div
          ref={contextMenuRef}
          role="menu"
          aria-label={`Tab actions for ${contextMenu.entry.path}`}
          className="fixed z-50 w-48 border border-border bg-background p-1 shadow-lg"
          style={{ left: contextMenu.x, top: contextMenu.y }}
          onContextMenu={event => event.preventDefault()}
        >
          <button type="button" role="menuitem" className="flex h-8 w-full items-center gap-2 px-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500" onClick={() => runContextAction(() => onClose(contextMenu.entry))}>
            <X className="size-3.5" />
            Close
          </button>
          <button type="button" role="menuitem" className="flex h-8 w-full items-center gap-2 px-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500 disabled:pointer-events-none disabled:opacity-40" disabled={tabs.length === 1} onClick={() => runContextAction(() => onCloseOthers(contextMenu.entry))}>
            <CircleX className="size-3.5" />
            Close Others
          </button>
          <button type="button" role="menuitem" className="flex h-8 w-full items-center gap-2 px-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500 disabled:pointer-events-none disabled:opacity-40" disabled={contextIndex === tabs.length - 1} onClick={() => runContextAction(() => onCloseToRight(contextMenu.entry))}>
            <X className="size-3.5" />
            Close to the Right
          </button>
          <div className="my-1 h-px bg-border" />
          <button type="button" role="menuitem" className="flex h-8 w-full items-center gap-2 px-2 text-left text-xs text-red-600 hover:bg-red-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-red-500 dark:text-red-400 dark:hover:bg-red-950/40" onClick={() => runContextAction(onCloseAll)}>
            <CircleX className="size-3.5" />
            Close All
          </button>
        </div>
      )}
    </>
  )
}
