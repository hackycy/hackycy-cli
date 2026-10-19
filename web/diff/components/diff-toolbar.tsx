import type { LucideIcon } from 'lucide-react'
import { Columns2, Menu, Moon, RefreshCw, Rows3, Space, Square, WrapText } from 'lucide-react'
import { Button } from '../../shared/components/ui/button'
import { Tooltip } from '../../shared/components/ui/tooltip'
import { cn } from '../../shared/lib/utils'

const activeClass = 'border-cyan-500/40 bg-cyan-500/10 text-foreground hover:bg-cyan-500/15'
const controlClass = 'px-2'

function ToolbarToggle({ label, description, icon: Icon, active, onToggle }: {
  label: string
  description: string
  icon: LucideIcon
  active: boolean
  onToggle: () => void
}): React.JSX.Element {
  return (
    <Tooltip label={description}>
      <Button aria-label={label} aria-pressed={active} className={cn(controlClass, active && activeClass)} onClick={onToggle}>
        <Icon aria-hidden="true" className="size-4" />
        <span aria-hidden="true" className={cn('rounded px-1 text-[10px] font-semibold', active ? 'bg-cyan-500/15' : 'bg-muted text-muted-foreground')}>
          {active ? 'On' : 'Off'}
        </span>
      </Button>
    </Tooltip>
  )
}

export function DiffToolbar({ refreshing, diffStyle, wrap, ignoreWhitespace, theme, onFiles, onRefresh, onDiffStyle, onWrap, onIgnoreWhitespace, onTheme }: {
  refreshing: boolean
  diffStyle: 'split' | 'unified'
  wrap: boolean
  ignoreWhitespace: boolean
  theme: 'light' | 'dark'
  onFiles: () => void
  onRefresh: () => void
  onDiffStyle: (style: 'split' | 'unified') => void
  onWrap: () => void
  onIgnoreWhitespace: () => void
  onTheme: () => void
}): React.JSX.Element {
  return (
    <div role="toolbar" aria-label="Diff controls" className="ml-auto flex max-w-full flex-wrap items-center gap-2">
      <Tooltip label="Browse comparison files.">
        <Button aria-label="Files" size="icon" className="min-[900px]:hidden" onClick={onFiles}>
          <Menu aria-hidden="true" className="size-4" />
        </Button>
      </Tooltip>
      <Tooltip label={refreshing ? 'Stop refreshing the directory comparison.' : 'Re-scan both directories and update the comparison.'}>
        <Button aria-label={refreshing ? 'Cancel comparison refresh' : 'Refresh comparison'} size="icon" onClick={onRefresh}>
          {refreshing ? <Square aria-hidden="true" className="size-4" /> : <RefreshCw aria-hidden="true" className="size-4" />}
        </Button>
      </Tooltip>
      <div role="group" aria-label="Diff layout" className="hidden items-center gap-0.5 rounded-md border border-border bg-muted/40 p-0.5 min-[900px]:flex">
        <Tooltip label="Show Baseline and Target side by side.">
          <Button aria-label="Side-by-side diff" aria-pressed={diffStyle === 'split'} size="icon" variant="ghost" className={cn('size-7', diffStyle === 'split' && 'border-border bg-background shadow-sm')} onClick={() => onDiffStyle('split')}>
            <Columns2 aria-hidden="true" className="size-4" />
          </Button>
        </Tooltip>
        <Tooltip label="Show removed and added lines together in one column.">
          <Button aria-label="Unified diff" aria-pressed={diffStyle === 'unified'} size="icon" variant="ghost" className={cn('size-7', diffStyle === 'unified' && 'border-border bg-background shadow-sm')} onClick={() => onDiffStyle('unified')}>
            <Rows3 aria-hidden="true" className="size-4" />
          </Button>
        </Tooltip>
      </div>
      <ToolbarToggle label="Wrap lines" description={`Line wrapping is ${wrap ? 'on' : 'off'}. Turn it on to fit long lines within the editor.`} icon={WrapText} active={wrap} onToggle={onWrap} />
      <ToolbarToggle label="Ignore whitespace" description={`Ignoring whitespace is ${ignoreWhitespace ? 'on' : 'off'}. On ignores spaces and tabs at line edges and line endings. Off shows exact differences.`} icon={Space} active={ignoreWhitespace} onToggle={onIgnoreWhitespace} />
      <ToolbarToggle label="Dark mode" description={`Dark mode is ${theme === 'dark' ? 'on' : 'off'}. Switch between dark and light appearance.`} icon={Moon} active={theme === 'dark'} onToggle={onTheme} />
    </div>
  )
}
