import type { ComponentProps, Ref } from 'react'
import * as ScrollAreaPrimitive from '@radix-ui/react-scroll-area'
import { cn } from '../../lib/utils'

type ScrollbarOrientation = 'vertical' | 'horizontal' | 'both' | 'none'

export interface ScrollAreaProps extends ComponentProps<typeof ScrollAreaPrimitive.Root> {
  viewportClassName?: string
  viewportProps?: ComponentProps<typeof ScrollAreaPrimitive.Viewport>
  viewportRef?: Ref<HTMLDivElement>
  scrollbars?: ScrollbarOrientation
}

export function ScrollArea({
  className,
  children,
  type = 'hover',
  viewportClassName,
  viewportProps,
  viewportRef,
  scrollbars = 'vertical',
  ...props
}: ScrollAreaProps): React.JSX.Element {
  const { className: viewportPropsClassName, ...restViewportProps } = viewportProps ?? {}

  return (
    <ScrollAreaPrimitive.Root type={type} className={cn('relative min-h-0', className)} {...props}>
      <ScrollAreaPrimitive.Viewport ref={viewportRef} className={cn('h-full w-full', viewportClassName, viewportPropsClassName)} {...restViewportProps}>
        {children}
      </ScrollAreaPrimitive.Viewport>
      {(scrollbars === 'vertical' || scrollbars === 'both') && <Scrollbar orientation="vertical" />}
      {(scrollbars === 'horizontal' || scrollbars === 'both') && <Scrollbar orientation="horizontal" />}
      {scrollbars === 'both' && <ScrollAreaPrimitive.Corner className="bg-background" />}
    </ScrollAreaPrimitive.Root>
  )
}

function Scrollbar({ orientation }: { orientation: 'vertical' | 'horizontal' }): React.JSX.Element {
  return (
    <ScrollAreaPrimitive.Scrollbar
      orientation={orientation}
      className={cn(
        'z-10 flex touch-none select-none p-px transition-opacity data-[state=hidden]:opacity-0',
        orientation === 'vertical' ? 'right-0 top-0 h-full w-[5px]' : 'bottom-0 left-0 h-[5px] w-full',
      )}
    >
      <ScrollAreaPrimitive.Thumb className="relative flex-1 rounded-full bg-border/80 hover:bg-muted-foreground/70" />
    </ScrollAreaPrimitive.Scrollbar>
  )
}
