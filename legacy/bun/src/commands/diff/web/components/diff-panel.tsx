import type { ComparisonSide, Entry, EntryDetail, TextContent } from '../api'
import { MultiFileDiff } from '@pierre/diffs/react'
import { AlertTriangle, FileWarning, ImageOff, Link as LinkIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Button } from '../../../../shared/web/components/ui/button'
import { cn } from '../../../../shared/web/lib/utils'
import { apiJson, blobUrl, contentUrl } from '../api'
import { contentCache, estimateCacheBytes } from '../lib/content-cache'

const statusLabel = {
  added: 'Added',
  deleted: 'Deleted',
  modified: 'Modified',
  unchanged: 'Unchanged',
  issue: 'Issue',
}

const statusLetter = {
  added: 'A',
  deleted: 'D',
  modified: 'M',
  unchanged: 'U',
  issue: '!',
}

const overlayScrollbarCss = `
  [data-code] {
    scrollbar-gutter: auto;
    scrollbar-width: none;
  }

  [data-code]::-webkit-scrollbar {
    height: 0;
  }
`

async function cachedJson<T>(key: string, url: string, signal: AbortSignal): Promise<T> {
  const cached = contentCache.get<T>(key)
  if (cached !== undefined)
    return cached
  const value = await apiJson<T>(url, { signal })
  if (!signal.aborted)
    contentCache.set(key, value, estimateCacheBytes(value))
  return value
}

function formatBytes(value: number | undefined): string {
  if (value === undefined)
    return '-'
  if (value < 1024)
    return `${value} B`
  if (value < 1024 * 1024)
    return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

function ImageSide({ snapshotId, entry, side }: { snapshotId: string, entry: Entry, side: ComparisonSide }): React.JSX.Element {
  const available = side === 'baseline' ? entry.baselineSize !== undefined : entry.targetSize !== undefined
  const [failed, setFailed] = useState(false)
  return (
    <div className="min-w-0 flex-1 border-r border-border last:border-r-0">
      <div className="flex h-8 items-center justify-between border-b border-border bg-muted/40 px-3 text-xs text-muted-foreground">
        <span>{side === 'baseline' ? 'Baseline' : 'Target'}</span>
        <span>{formatBytes(side === 'baseline' ? entry.baselineSize : entry.targetSize)}</span>
      </div>
      <div className="flex min-h-52 items-center justify-center overflow-auto bg-image-grid p-5">
        {!available || failed
          ? (
              <div className="flex flex-col items-center gap-2 text-xs text-muted-foreground">
                <ImageOff className="size-5" />
                <span>{available ? 'Preview failed' : 'Not present'}</span>
              </div>
            )
          : <img className="max-h-[520px] max-w-full object-contain" src={blobUrl(snapshotId, entry.id, side)} alt={`${side} ${entry.path}`} onError={() => setFailed(true)} />}
      </div>
    </div>
  )
}

export function DiffPanel({
  entry,
  snapshotId,
  diffStyle,
  wrap,
  ignoreWhitespace,
  theme,
}: {
  entry: Entry
  snapshotId: string
  diffStyle: 'split' | 'unified'
  wrap: boolean
  ignoreWhitespace: boolean
  theme: 'light' | 'dark'
}): React.JSX.Element {
  const [detail, setDetail] = useState<EntryDetail>()
  const [contents, setContents] = useState<Partial<Record<ComparisonSide, TextContent>>>({})
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setDetail(undefined)
    setContents({})
    cachedJson<EntryDetail>(
      `${snapshotId}:${entry.id}:detail`,
      `/api/entries/${entry.id}?snapshot=${encodeURIComponent(snapshotId)}`,
      controller.signal,
    )
      .then(async (nextDetail) => {
        setDetail(nextDetail)
        if (nextDetail.presentation !== 'text')
          return
        const [baseline, target] = await Promise.all([
          cachedJson<TextContent>(`${snapshotId}:${entry.id}:baseline`, contentUrl(snapshotId, entry.id, 'baseline'), controller.signal),
          cachedJson<TextContent>(`${snapshotId}:${entry.id}:target`, contentUrl(snapshotId, entry.id, 'target'), controller.signal),
        ])
        setContents({ baseline, target })
      })
      .catch(() => setDetail(current => current ?? (entry.status === 'issue'
        ? { ...entry, presentation: 'issue' }
        : { ...entry, presentation: 'stale' })))
      .finally(() => setLoading(false))
    return () => controller.abort()
  }, [entry, snapshotId])

  const forceContent = async (): Promise<void> => {
    const [baseline, target] = await Promise.all([
      apiJson<TextContent>(contentUrl(snapshotId, entry.id, 'baseline', true)),
      apiJson<TextContent>(contentUrl(snapshotId, entry.id, 'target', true)),
    ])
    contentCache.set(`${snapshotId}:${entry.id}:baseline`, baseline, estimateCacheBytes(baseline))
    contentCache.set(`${snapshotId}:${entry.id}:target`, target, estimateCacheBytes(target))
    setContents({ baseline, target })
  }

  const baseline = contents.baseline
  const target = contents.target
  const guarded = baseline?.status === 'guarded' || target?.status === 'guarded'
  const blocked = baseline?.status === 'blocked' || target?.status === 'blocked'
  const stale = detail?.presentation === 'stale' || baseline?.status === 'stale' || target?.status === 'stale'
  const baselineSize = detail?.baselineSize ?? entry.baselineSize
  const targetSize = detail?.targetSize ?? entry.targetSize
  const encodingOnly = entry.status === 'modified'
    && baseline?.status === 'ready'
    && target?.status === 'ready'
    && baseline.text === target.text

  return (
    <section id={`entry-${entry.id}`} data-diff-editor className="flex min-h-full flex-col bg-background">
      <header data-diff-status-bar className="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-muted/20 px-3">
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-semibold" title={entry.path}>{entry.path}</span>
        <span className={cn('status-badge gap-1', `status-${entry.status}`)} aria-label={`Status: ${statusLabel[entry.status]}`}>
          <span aria-hidden="true" className="font-mono">{statusLetter[entry.status]}</span>
          <span>{statusLabel[entry.status]}</span>
        </span>
        <span className="hidden text-xs tabular-nums text-muted-foreground sm:inline">
          {formatBytes(baselineSize)}
          {' '}
          →
          {' '}
          {formatBytes(targetSize)}
        </span>
      </header>
      <div className="flex min-h-0 flex-1 flex-col">
        {loading && <div className="min-h-36 flex-1 animate-pulse bg-muted/50" />}
        {!loading && stale && <PanelMessage icon={AlertTriangle} title="Snapshot is stale" />}
        {!loading && detail?.presentation === 'issue' && <PanelMessage icon={AlertTriangle} title={detail.message ?? 'Entry could not be compared'} />}
        {!loading && detail?.presentation === 'image' && (
          <div className="flex flex-1 flex-col sm:flex-row">
            <ImageSide snapshotId={snapshotId} entry={detail} side="baseline" />
            <ImageSide snapshotId={snapshotId} entry={detail} side="target" />
          </div>
        )}
        {!loading && detail?.presentation === 'binary' && <PanelMessage icon={FileWarning} title="Binary files differ" />}
        {!loading && detail?.presentation === 'symlink' && (
          <div className="grid min-h-24 flex-1 grid-cols-1 divide-y divide-border sm:grid-cols-2 sm:divide-x sm:divide-y-0">
            <LinkTarget label="Baseline" value={detail.baselineLinkTarget} />
            <LinkTarget label="Target" value={detail.targetLinkTarget} />
          </div>
        )}
        {!loading && (detail?.presentation === 'oversized' || blocked) && <PanelMessage icon={FileWarning} title="Text diff exceeds the rendering limit" />}
        {!loading && guarded && (
          <div className="flex min-h-28 flex-1 items-center justify-center"><Button onClick={() => void forceContent()}>Render large diff</Button></div>
        )}
        {!loading && encodingOnly && <PanelMessage icon={FileWarning} title="Only text encoding or BOM differs" />}
        {!loading && detail?.presentation === 'text' && !guarded && !blocked && !encodingOnly && baseline && target && (
          <div className="min-h-0 flex-1">
            <MultiFileDiff
              oldFile={{ name: entry.path, contents: baseline.status === 'ready' ? baseline.text : '', cacheKey: `${snapshotId}:${entry.id}:baseline` }}
              newFile={{ name: entry.path, contents: target.status === 'ready' ? target.text : '', cacheKey: `${snapshotId}:${entry.id}:target` }}
              options={{
                diffStyle,
                overflow: wrap ? 'wrap' : 'scroll',
                disableFileHeader: true,
                themeType: theme,
                unsafeCSS: overlayScrollbarCss,
                parseDiffOptions: ignoreWhitespace ? { ignoreWhitespace: true } : undefined,
              }}
            />
          </div>
        )}
      </div>
    </section>
  )
}

function PanelMessage({ icon: Icon, title }: { icon: typeof AlertTriangle, title: string }): React.JSX.Element {
  return (
    <div className="flex min-h-28 flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
      <Icon className="size-4" />
      <span>{title}</span>
    </div>
  )
}

function LinkTarget({ label, value }: { label: string, value?: string }): React.JSX.Element {
  return (
    <div className="min-w-0 p-4">
      <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <LinkIcon className="size-3.5" />
        {label}
      </div>
      <code className="block overflow-x-auto whitespace-pre text-xs">{value ?? 'Not present'}</code>
    </div>
  )
}
