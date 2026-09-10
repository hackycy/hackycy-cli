import type { FormEventHandler, ReactNode, Ref } from 'react'
import * as AlertDialog from '@radix-ui/react-alert-dialog'
import * as Dialog from '@radix-ui/react-dialog'
import * as ScrollArea from '@radix-ui/react-scroll-area'
import * as SelectPrimitive from '@radix-ui/react-select'
import * as SwitchPrimitive from '@radix-ui/react-switch'
import * as Tabs from '@radix-ui/react-tabs'
import * as Toast from '@radix-ui/react-toast'
import * as ToggleGroup from '@radix-ui/react-toggle-group'
import * as Tooltip from '@radix-ui/react-tooltip'
import { Check, CheckCircle2, ChevronDown, LoaderCircle, X, XCircle } from 'lucide-react'
import { createContext, useCallback, useContext, useState } from 'react'

interface Notification {
  id: number
  kind: 'success' | 'error'
  message: string
  duration: number
}

interface Feedback {
  notify: (message: string, kind?: Notification['kind'], duration?: number) => void
}

const FeedbackContext = createContext<Feedback | null>(null)
const DEFAULT_NOTIFICATION_DURATION = 5_000

export function FeedbackProvider({ children }: { children: ReactNode }): React.JSX.Element {
  const [notifications, setNotifications] = useState<Notification[]>([])
  const dismiss = useCallback((id: number): void => setNotifications(values => values.filter(value => value.id !== id)), [])
  const notify = useCallback((message: string, kind: Notification['kind'] = 'success', duration = DEFAULT_NOTIFICATION_DURATION): void => {
    const id = Date.now() + Math.random()
    setNotifications(values => [...values.slice(-3), { id, kind, message, duration }])
  }, [])

  return (
    <Tooltip.Provider delayDuration={350}>
      <Toast.Provider duration={DEFAULT_NOTIFICATION_DURATION} swipeDirection="right">
        <FeedbackContext value={{ notify }}>
          {children}
          <Toast.Viewport className="notification-region" />
          {notifications.map(notification => (
            <Toast.Root
              className={`notification notification-${notification.kind}`}
              key={notification.id}
              open
              duration={notification.duration}
              onOpenChange={(open) => {
                if (!open)
                  dismiss(notification.id)
              }}
            >
              {notification.kind === 'success' ? <CheckCircle2 size={16} /> : <XCircle size={16} />}
              <Toast.Description className="notification-message">{notification.message}</Toast.Description>
              <Toast.Close asChild>
                <IconButton label="Dismiss notification" onClick={() => dismiss(notification.id)}><X size={14} /></IconButton>
              </Toast.Close>
            </Toast.Root>
          ))}
        </FeedbackContext>
      </Toast.Provider>
    </Tooltip.Provider>
  )
}

export function useFeedback(): Feedback {
  const value = useContext(FeedbackContext)
  if (!value)
    throw new Error('FeedbackProvider is required')
  return value
}

export function Spinner({ size = 15 }: { size?: number }): React.JSX.Element {
  return <LoaderCircle className="spinner" size={size} aria-hidden="true" />
}

export function IconButton({ label, children, loading = false, disabled, ...props }: { label: string, children: ReactNode, loading?: boolean } & React.ButtonHTMLAttributes<HTMLButtonElement>): React.JSX.Element {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        <button type="button" className="icon-button" aria-label={label} aria-busy={loading || undefined} disabled={disabled || loading} {...props}>{loading ? <Spinner /> : children}</button>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="tooltip-content" sideOffset={6}>{label}</Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}

export function Switch({ label, checked, disabled, loading = false, onChange }: { label: string, checked: boolean, disabled?: boolean, loading?: boolean, onChange: (checked: boolean) => void }): React.JSX.Element {
  return (
    <SwitchPrimitive.Root
      className="switch"
      checked={checked}
      aria-label={label}
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      onCheckedChange={onChange}
    >
      <SwitchPrimitive.Thumb className="switch-thumb">{loading && <Spinner size={11} />}</SwitchPrimitive.Thumb>
    </SwitchPrimitive.Root>
  )
}

export interface SelectOption {
  value: string
  label: string
}

export function Select({ label, value, options, disabled, onChange }: { label: string, value: string, options: SelectOption[], disabled?: boolean, onChange: (value: string) => void }): React.JSX.Element {
  const emptyValue = '__radix_select_empty__'
  return (
    <SelectPrimitive.Root value={value || emptyValue} disabled={disabled} onValueChange={next => onChange(next === emptyValue ? '' : next)}>
      <SelectPrimitive.Trigger className="select-trigger" aria-label={label}>
        <SelectPrimitive.Value />
        <SelectPrimitive.Icon><ChevronDown size={15} /></SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content className="select-content" position="popper" sideOffset={4}>
          <SelectPrimitive.Viewport>
            {options.map(option => (
              <SelectPrimitive.Item className="select-item" key={option.value || emptyValue} value={option.value || emptyValue}>
                <SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText>
                <SelectPrimitive.ItemIndicator><Check size={14} /></SelectPrimitive.ItemIndicator>
              </SelectPrimitive.Item>
            ))}
          </SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  )
}

export function SegmentedControl({ label, value, options, disabled, className, onChange }: { label: string, value: string, options: SelectOption[], disabled?: boolean, className?: string, onChange: (value: string) => void }): React.JSX.Element {
  return (
    <ToggleGroup.Root
      aria-label={label}
      className={['segmented', className].filter(Boolean).join(' ')}
      type="single"
      value={value}
      disabled={disabled}
      onValueChange={(next) => {
        if (next)
          onChange(next)
      }}
    >
      {options.map(option => <ToggleGroup.Item key={option.value} value={option.value}>{option.label}</ToggleGroup.Item>)}
    </ToggleGroup.Root>
  )
}

export { Tabs }

export function FormScrollArea({ children }: { children: ReactNode }): React.JSX.Element {
  return (
    <ScrollArea.Root className="tunnel-step-scroll">
      <ScrollArea.Viewport className="tunnel-step-viewport">{children}</ScrollArea.Viewport>
      <ScrollArea.Scrollbar className="tunnel-step-scrollbar" orientation="vertical">
        <ScrollArea.Thumb className="tunnel-step-scroll-thumb" />
      </ScrollArea.Scrollbar>
    </ScrollArea.Root>
  )
}

export interface DialogShellProps {
  open: boolean
  title: string
  children: ReactNode
  onOpenChange: (open: boolean) => void
  onSubmit?: FormEventHandler<HTMLFormElement>
  className?: string
  formClassName?: string
  formRef?: Ref<HTMLFormElement>
  busy?: boolean
  closeLabel?: string
  onOpenAutoFocus?: (event: Event) => void
}

export function DialogShell({
  open,
  title,
  children,
  onOpenChange,
  onSubmit,
  className,
  formClassName,
  formRef,
  busy = false,
  closeLabel = 'Close',
  onOpenAutoFocus,
}: DialogShellProps): React.JSX.Element {
  const close = (next: boolean): void => {
    if (!busy || next)
      onOpenChange(next)
  }

  return (
    <Dialog.Root open={open} onOpenChange={close}>
      <Dialog.Portal>
        <Dialog.Overlay className="modal-backdrop" />
        <Dialog.Content
          className={['modal', className].filter(Boolean).join(' ')}
          onEscapeKeyDown={event => busy && event.preventDefault()}
          onPointerDownOutside={event => busy && event.preventDefault()}
          onOpenAutoFocus={onOpenAutoFocus}
        >
          <form ref={formRef} className={['modal-form', formClassName].filter(Boolean).join(' ')} aria-busy={busy || undefined} onSubmit={onSubmit}>
            <div className="modal-title">
              <Dialog.Title>{title}</Dialog.Title>
              <Dialog.Close asChild>
                <IconButton label={closeLabel} disabled={busy}><X size={16} /></IconButton>
              </Dialog.Close>
            </div>
            {children}
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

export function ConfirmDialog({ open, message, busy, error, onClose, onConfirm }: { open: boolean, message: string, busy: boolean, error?: string, onClose: () => void, onConfirm: () => void }): React.JSX.Element {
  return (
    <AlertDialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy)
          onClose()
      }}
    >
      <AlertDialog.Portal>
        <AlertDialog.Overlay className="modal-backdrop" />
        <AlertDialog.Content className="modal">
          <div className="modal-title">
            <AlertDialog.Title>Confirm action</AlertDialog.Title>
            <AlertDialog.Cancel asChild><IconButton label="Close" disabled={busy}><X size={16} /></IconButton></AlertDialog.Cancel>
          </div>
          <AlertDialog.Description>{message}</AlertDialog.Description>
          {error && <p className="form-error" role="alert">{error}</p>}
          <div className="modal-actions">
            <AlertDialog.Cancel asChild><button type="button" disabled={busy}>Cancel</button></AlertDialog.Cancel>
            <button className="danger" type="button" disabled={busy} onClick={onConfirm}>
              {busy && <Spinner />}
              {busy ? 'Working...' : 'Confirm'}
            </button>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  )
}
