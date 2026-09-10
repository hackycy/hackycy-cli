import type { DirectoryEntry } from '../api'
import {
  File,
  FileArchive,
  FileText,
  Film,
  Folder,
  Image,
  Music,
  ShieldAlert,
} from 'lucide-react'
import { cn } from '../../../../shared/web/lib/utils'

export function EntryGlyph({ entry, className }: { entry: DirectoryEntry, className?: string }): React.JSX.Element {
  const iconClass = cn('shrink-0', className)
  if (entry.kind === 'unavailable')
    return <ShieldAlert className={cn(iconClass, 'text-amber-600 dark:text-amber-400')} />
  if (entry.kind === 'directory')
    return <Folder className={cn(iconClass, 'fill-folder/30 text-folder')} />
  if (entry.extractable)
    return <FileArchive className={cn(iconClass, 'text-blue-600 dark:text-blue-400')} />
  if (entry.previewKind === 'image')
    return <Image className={cn(iconClass, 'text-teal-600 dark:text-teal-400')} />
  if (entry.previewKind === 'video')
    return <Film className={cn(iconClass, 'text-fuchsia-600 dark:text-fuchsia-400')} />
  if (entry.previewKind === 'audio')
    return <Music className={cn(iconClass, 'text-violet-600 dark:text-violet-400')} />
  if (entry.previewKind === 'text')
    return <FileText className={cn(iconClass, 'text-zinc-600 dark:text-zinc-300')} />
  return <File className={cn(iconClass, 'text-zinc-500')} />
}
