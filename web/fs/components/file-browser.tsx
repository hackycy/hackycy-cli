import type { DirectoryEntry } from '../api'
import type { SortDirection, SortKey, ViewMode } from '../types'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { useVirtualizer } from '@tanstack/react-virtual'
import {
  ArchiveRestore,
  ArrowDown,
  ArrowUp,
  ClipboardPaste,
  Copy,
  Download,
  Eye,
  Folder,
  FolderOpen,
  FolderPlus,
  Link as LinkIcon,
  Pencil,
  RefreshCw,
  Scissors,
  Trash2,
} from 'lucide-react'
import { useLayoutEffect, useRef, useState } from 'react'
import { ScrollArea } from '../../shared/components/ui/scroll-area'
import { cn } from '../../shared/lib/utils'
import { EntryGlyph } from './entry-glyph'

export function formatFileSize(bytes: number | undefined): string {
  if (bytes === undefined)
    return '-'
  if (bytes === 0)
    return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** unit
  return `${value.toFixed(unit === 0 ? 0 : value >= 10 ? 0 : 1)} ${units[unit]}`
}

const fileDateFormatter = new Intl.DateTimeFormat('en-US', {
  year: 'numeric',
  month: 'short',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
})

export function formatDate(value: string | undefined): string {
  if (!value)
    return '-'
  return fileDateFormatter.format(new Date(value))
}

function formatFileType(entry: DirectoryEntry): string {
  if (entry.kind === 'directory')
    return 'File folder'
  if (entry.kind === 'unavailable')
    return 'Unavailable'
  if (entry.previewKind === 'image')
    return 'Image'
  if (entry.previewKind === 'video')
    return 'Video'
  if (entry.previewKind === 'audio')
    return 'Audio'
  if (entry.previewKind === 'pdf')
    return 'PDF document'
  if (entry.previewKind === 'text')
    return 'Text document'
  return entry.mimeType?.split('/').at(-1)?.toUpperCase() ?? 'File'
}

function EntryVisual({ entry, large = false }: { entry: DirectoryEntry, large?: boolean }): React.JSX.Element {
  const [thumbnailFailed, setThumbnailFailed] = useState(false)
  if (entry.previewKind === 'image' && entry.thumbnailUrl && !thumbnailFailed) {
    return (
      <span className={cn('image-thumbnail overflow-hidden border border-border bg-muted', large ? 'h-[72px] w-full rounded-md' : 'size-7 shrink-0 rounded')}>
        <img
          src={entry.thumbnailUrl}
          alt=""
          draggable={false}
          loading="lazy"
          decoding="async"
          fetchPriority="low"
          className="h-full w-full object-cover"
          onError={() => setThumbnailFailed(true)}
        />
      </span>
    )
  }
  return (
    <span className={cn('flex shrink-0 items-center justify-center', large ? 'h-[72px] w-full' : 'size-7')}>
      <EntryGlyph entry={entry} className={large ? 'size-9' : 'size-[18px]'} />
    </span>
  )
}

function SortHeading({ label, sortKey, activeKey, direction, className, onSort }: {
  label: string
  sortKey: SortKey
  activeKey: SortKey
  direction: SortDirection
  className?: string
  onSort: (key: SortKey) => void
}): React.JSX.Element {
  const active = activeKey === sortKey
  return (
    <button type="button" className={cn('details-heading', className)} onClick={() => onSort(sortKey)}>
      <span>{label}</span>
      {active && (direction === 'asc' ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" />)}
    </button>
  )
}

interface FileBrowserProps {
  entries: DirectoryEntry[]
  viewMode: ViewMode
  sortKey: SortKey
  direction: SortDirection
  selectedPaths: Set<string>
  managementEnabled: boolean
  canPaste: boolean
  creatingFolder: boolean
  editingBusy: boolean
  editingError?: string
  emptyLabel?: string
  onSort: (key: SortKey) => void
  onSelect: (entry: DirectoryEntry, modifiers: { toggle: boolean, range: boolean }) => void
  onOpen: (entry: DirectoryEntry) => void
  onCreateFolder: (name: string) => void
  onCancelEdit: () => void
  onCut: (entry?: DirectoryEntry) => void
  onCopy: (entry?: DirectoryEntry) => void
  onPaste: (destinationPath?: string) => void
  onStartRename: (entry: DirectoryEntry) => void
  onDelete: (entry?: DirectoryEntry) => void
  onExtract: (entry?: DirectoryEntry) => void
  onNewFolder: () => void
  onRefresh: () => void
}

export function FileBrowser(props: FileBrowserProps): React.JSX.Element {
  const view = props.viewMode === 'grid' ? <GridView {...props} /> : <ListView {...props} />
  return (
    <ContextMenu.Root modal={false}>
      <ContextMenu.Trigger asChild>
        <div className="file-browser relative h-full min-h-0 w-full" role="listbox" aria-label="Files" aria-multiselectable="true">
          {props.emptyLabel
            ? (
                <div className="centered-state">
                  <FolderOpen className="size-7" />
                  <span>{props.emptyLabel}</span>
                </div>
              )
            : view}
        </div>
      </ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content className="menu-content" aria-label="Folder actions">
          {props.managementEnabled && <MenuItem icon={<FolderPlus />} label="New folder" onSelect={props.onNewFolder} />}
          {props.managementEnabled && <MenuItem icon={<ClipboardPaste />} label="Paste" disabled={!props.canPaste} onSelect={() => props.onPaste()} />}
          {props.managementEnabled && <ContextMenu.Separator className="menu-separator" />}
          <MenuItem icon={<RefreshCw />} label="Refresh" onSelect={props.onRefresh} />
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  )
}

function ListView(props: FileBrowserProps): React.JSX.Element {
  const viewportRef = useRef<HTMLDivElement>(null)
  const offset = props.creatingFolder ? 1 : 0
  const rowHeight = matchMedia('(max-width: 899px)').matches ? 44 : 40
  const virtualizer = useVirtualizer({
    count: props.entries.length + offset,
    getScrollElement: () => viewportRef.current,
    estimateSize: () => rowHeight,
    overscan: 4,
  })
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="details-grid details-header">
        <SortHeading label="Name" sortKey="name" activeKey={props.sortKey} direction={props.direction} onSort={props.onSort} />
        <SortHeading label="Date modified" sortKey="modified" activeKey={props.sortKey} direction={props.direction} onSort={props.onSort} />
        <span className="details-heading">Type</span>
        <SortHeading label="Size" sortKey="size" activeKey={props.sortKey} direction={props.direction} className="justify-end" onSort={props.onSort} />
      </div>
      <ScrollArea className="min-h-0 flex-1" viewportRef={viewportRef}>
        <div className="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
          {virtualizer.getVirtualItems().map((row) => {
            const creating = props.creatingFolder && row.index === 0
            const entry = creating ? undefined : props.entries[row.index - offset]
            return (
              <div key={creating ? 'new-folder' : entry!.path} className="virtual-list-row absolute left-0 w-full" style={{ height: `${row.size}px`, transform: `translateY(${row.start}px)` }}>
                {creating
                  ? <NewFolderRow large={false} busy={props.editingBusy} error={props.editingError} onCommit={props.onCreateFolder} onCancel={props.onCancelEdit} />
                  : <ListEntry entry={entry!} props={props} />}
              </div>
            )
          })}
        </div>
      </ScrollArea>
    </div>
  )
}

function ListEntry({ entry, props }: { entry: DirectoryEntry, props: FileBrowserProps }): React.JSX.Element {
  const selected = props.selectedPaths.has(entry.path)
  return (
    <EntryContextMenu entry={entry} props={props}>
      <div
        role="option"
        aria-selected={selected}
        tabIndex={selected ? 0 : -1}
        className={cn('details-grid details-row', selected && 'selected')}
        title={entry.kind === 'unavailable' ? 'This entry cannot be opened' : entry.name}
        onClick={(event) => {
          const modifiers = { toggle: event.metaKey || event.ctrlKey, range: event.shiftKey }
          props.onSelect(entry, modifiers)
          if (!modifiers.toggle && !modifiers.range && entry.kind === 'file')
            props.onOpen(entry)
        }}
        onDoubleClick={() => entry.kind !== 'unavailable' && props.onOpen(entry)}
        onKeyDown={event => event.key === 'Enter' && entry.kind !== 'unavailable' && props.onOpen(entry)}
      >
        <span className="flex min-w-0 items-center gap-2">
          <EntryVisual entry={entry} />
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="truncate text-xs">{entry.name}</span>
            {entry.isSymlink && <LinkIcon aria-label="Symbolic link" className="size-3 shrink-0 text-muted-foreground" />}
          </span>
        </span>
        <span className="details-cell">{formatDate(entry.modifiedAt)}</span>
        <span className="details-cell truncate">{formatFileType(entry)}</span>
        <span className="details-cell text-right tabular-nums">{formatFileSize(entry.size)}</span>
      </div>
    </EntryContextMenu>
  )
}

function GridView(props: FileBrowserProps): React.JSX.Element {
  const viewportRef = useRef<HTMLDivElement>(null)
  const [columns, setColumns] = useState(0)
  useLayoutEffect(() => {
    const element = viewportRef.current
    if (!element)
      return
    const updateColumns = (width: number): void => {
      const next = width >= 1200 ? 7 : width >= 980 ? 6 : width >= 780 ? 5 : width >= 580 ? 4 : width >= 390 ? 3 : 2
      setColumns(current => current === next ? current : next)
    }
    updateColumns(element.clientWidth)
    const observer = new ResizeObserver(([entry]) => updateColumns(entry?.contentRect.width ?? element.clientWidth))
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  const itemCount = props.entries.length + (props.creatingFolder ? 1 : 0)
  const rowCount = columns === 0 ? 0 : Math.ceil(itemCount / columns)
  const virtualizer = useVirtualizer({ count: rowCount, getScrollElement: () => viewportRef.current, estimateSize: () => 136, overscan: 1 })
  return (
    <ScrollArea className="h-full" viewportRef={viewportRef}>
      <div className="relative w-full px-3" style={{ height: `${virtualizer.getTotalSize() + 16}px` }}>
        {virtualizer.getVirtualItems().map((row) => {
          const start = row.index * columns
          const rowItems = Array.from({ length: columns }, (_, offset) => start + offset).filter(index => index < itemCount)
          return (
            <div key={row.key} className="virtual-grid-row absolute left-3 right-3 grid gap-1" style={{ height: `${row.size}px`, gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, transform: `translateY(${row.start + 8}px)` }}>
              {rowItems.map((index) => {
                const creating = props.creatingFolder && index === 0
                const entry = creating ? undefined : props.entries[index - (props.creatingFolder ? 1 : 0)]
                return creating
                  ? <NewFolderRow key="new-folder" large busy={props.editingBusy} error={props.editingError} onCommit={props.onCreateFolder} onCancel={props.onCancelEdit} />
                  : <GridEntry key={entry!.path} entry={entry!} props={props} />
              })}
            </div>
          )
        })}
      </div>
    </ScrollArea>
  )
}

function GridEntry({ entry, props }: { entry: DirectoryEntry, props: FileBrowserProps }): React.JSX.Element {
  const selected = props.selectedPaths.has(entry.path)
  return (
    <EntryContextMenu entry={entry} props={props}>
      <div
        role="option"
        aria-selected={selected}
        tabIndex={selected ? 0 : -1}
        className={cn('grid-entry', selected && 'selected')}
        onClick={(event) => {
          const modifiers = { toggle: event.metaKey || event.ctrlKey, range: event.shiftKey }
          props.onSelect(entry, modifiers)
          if (!modifiers.toggle && !modifiers.range && entry.kind === 'file')
            props.onOpen(entry)
        }}
        onDoubleClick={() => entry.kind !== 'unavailable' && props.onOpen(entry)}
        onKeyDown={event => event.key === 'Enter' && entry.kind !== 'unavailable' && props.onOpen(entry)}
      >
        <EntryVisual entry={entry} large />
        <span className="min-w-0 text-center">
          <span className="flex min-w-0 items-center justify-center gap-1">
            <span className="truncate text-xs">{entry.name}</span>
            {entry.isSymlink && <LinkIcon aria-label="Symbolic link" className="size-3 shrink-0 text-muted-foreground" />}
          </span>
          <span className="mt-0.5 block truncate text-[10px] text-muted-foreground">{entry.kind === 'directory' ? 'File folder' : formatFileSize(entry.size)}</span>
        </span>
      </div>
    </EntryContextMenu>
  )
}

function NewFolderRow({ large, busy, error, onCommit, onCancel }: { large: boolean, busy: boolean, error?: string, onCommit: (name: string) => void, onCancel: () => void }): React.JSX.Element {
  return (
    <div className={large ? 'grid-entry selected' : 'details-grid details-row selected'}>
      <span className={large ? 'flex min-w-0 flex-col items-center gap-2' : 'flex min-w-0 items-center gap-2'}>
        <span className={large ? 'flex h-[72px] items-center justify-center' : ''}><Folder className={cn('fill-folder/30 text-folder', large ? 'size-9' : 'size-[18px]')} /></span>
        <InlineNameEditor initialName="New folder" busy={busy} error={error} onCommit={onCommit} onCancel={onCancel} />
      </span>
    </div>
  )
}

function InlineNameEditor({ initialName, busy, error, onCommit, onCancel }: { initialName: string, busy: boolean, error?: string, onCommit: (name: string) => void, onCancel: () => void }): React.JSX.Element {
  const [name, setName] = useState(initialName)
  const submitted = useRef(false)
  const commit = (): void => {
    if (busy || submitted.current)
      return
    submitted.current = true
    onCommit(name)
    queueMicrotask(() => {
      submitted.current = false
    })
  }
  return (
    <input
      autoFocus
      aria-label="Entry name"
      aria-invalid={error !== undefined}
      title={error}
      className={cn('name-editor', error && 'invalid')}
      value={name}
      disabled={busy}
      onChange={event => setName(event.target.value)}
      onClick={event => event.stopPropagation()}
      onDoubleClick={event => event.stopPropagation()}
      onFocus={event => event.currentTarget.select()}
      onBlur={commit}
      onKeyDown={(event) => {
        event.stopPropagation()
        if (event.key === 'Enter')
          commit()
        if (event.key === 'Escape')
          onCancel()
      }}
    />
  )
}

function EntryContextMenu({ entry, props, children }: { entry: DirectoryEntry, props: FileBrowserProps, children: React.ReactNode }): React.JSX.Element {
  return (
    <ContextMenu.Root modal={false} onOpenChange={open => open && props.onSelect(entry, { toggle: false, range: false })}>
      <ContextMenu.Trigger asChild>{children}</ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content className="menu-content" aria-label={`Actions for ${entry.name}`}>
          {entry.kind !== 'unavailable' && <MenuItem icon={<Eye />} label="Open" onSelect={() => props.onOpen(entry)} />}
          {entry.kind === 'file' && entry.downloadUrl && <MenuItem icon={<Download />} label="Download" onSelect={() => window.location.assign(entry.downloadUrl!)} />}
          {props.managementEnabled && entry.extractable && <MenuItem icon={<ArchiveRestore />} label="Extract" onSelect={() => props.onExtract(entry)} />}
          {props.managementEnabled && <ContextMenu.Separator className="menu-separator" />}
          {props.managementEnabled && <MenuItem icon={<Scissors />} label="Cut" onSelect={() => props.onCut(entry)} />}
          {props.managementEnabled && <MenuItem icon={<Copy />} label="Copy" onSelect={() => props.onCopy(entry)} />}
          {props.managementEnabled && <MenuItem icon={<ClipboardPaste />} label="Paste" disabled={!props.canPaste || entry.kind !== 'directory'} onSelect={() => props.onPaste(entry.path)} />}
          {props.managementEnabled && <MenuItem icon={<Pencil />} label="Rename" onSelect={() => props.onStartRename(entry)} />}
          {props.managementEnabled && <ContextMenu.Separator className="menu-separator" />}
          {props.managementEnabled && <MenuItem destructive icon={<Trash2 />} label="Delete" onSelect={() => props.onDelete(entry)} />}
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  )
}

function MenuItem({ icon, label, destructive = false, disabled = false, onSelect }: { icon: React.ReactElement<{ className?: string }>, label: string, destructive?: boolean, disabled?: boolean, onSelect: () => void }): React.JSX.Element {
  return (
    <ContextMenu.Item className={cn('menu-item', destructive && 'destructive')} disabled={disabled} onSelect={onSelect}>
      {icon}
      <span>{label}</span>
    </ContextMenu.Item>
  )
}
