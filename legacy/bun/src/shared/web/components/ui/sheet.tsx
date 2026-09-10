import type { ComponentProps, ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '../../lib/utils'
import { Button } from './button'

export const Sheet = Dialog.Root
export const SheetTrigger = Dialog.Trigger

interface SheetContentProps extends ComponentProps<typeof Dialog.Content> {
  title: string
  description?: string
  side?: 'left' | 'right'
  closeLabel?: string
  header?: ReactNode
}

export function SheetContent({
  className,
  children,
  title,
  description,
  side = 'left',
  closeLabel = 'Close',
  header,
  ...props
}: SheetContentProps): React.JSX.Element {
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-40 bg-black/35" />
      <Dialog.Content
        className={cn(
          'fixed inset-y-0 z-50 w-[min(92vw,560px)] border-border bg-background shadow-xl outline-none',
          side === 'left' ? 'left-0 border-r' : 'right-0 border-l',
          className,
        )}
        {...props}
      >
        <Dialog.Title className={header ? undefined : 'sr-only'}>{title}</Dialog.Title>
        {description && <Dialog.Description className="sr-only">{description}</Dialog.Description>}
        {header}
        {children}
        <Dialog.Close asChild>
          <Button className="absolute right-2 top-2 z-10" size="icon" variant="ghost" aria-label={closeLabel}>
            <X className="size-4" />
          </Button>
        </Dialog.Close>
      </Dialog.Content>
    </Dialog.Portal>
  )
}
