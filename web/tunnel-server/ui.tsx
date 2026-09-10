import type { ReactNode } from 'react'
import { Check, Clipboard, RefreshCw, XCircle } from 'lucide-react'
import { useState } from 'react'
import { AdminPageHeader } from '../shared/admin'
import { ConfirmDialog, FeedbackProvider, IconButton, Spinner, Switch, useFeedback } from './primitives'

export { FeedbackProvider, IconButton, Spinner, Switch, useFeedback }

export function navigate(path: string): void {
  history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

function statusClass(value: string): string {
  if (['connected', 'running', 'Applied'].includes(value))
    return 'status status-good'
  if (['recovering', 'Pending', 'revocation_pending'].includes(value))
    return 'status status-warn'
  if (['incompatible', 'configuration_failed', 'Error'].includes(value))
    return 'status status-error'
  return 'status status-muted'
}

export function Status({ value }: { value: string }): React.JSX.Element {
  return <span className={statusClass(value)}>{value.replaceAll('_', ' ')}</span>
}

export function LoadingState({ label }: { label: string }): React.JSX.Element {
  return (
    <div className="load-state" role="status" aria-live="polite">
      <Spinner size={18} />
      <span>{label}</span>
    </div>
  )
}

export function ErrorState({ message, retrying = false, onRetry }: { message: string, retrying?: boolean, onRetry?: () => void }): React.JSX.Element {
  return (
    <div className="error-state" role="alert">
      <XCircle size={17} />
      <span>{message}</span>
      {onRetry && (
        <button type="button" disabled={retrying} onClick={onRetry}>
          {retrying ? <Spinner /> : <RefreshCw size={14} />}
          Retry
        </button>
      )}
    </div>
  )
}

export interface ConfirmAction {
  message: string
  action: () => Promise<void>
  successMessage?: string
}

export function ConfirmationDialog({ request, onClose }: { request: ConfirmAction, onClose: () => void }): React.JSX.Element {
  const { notify } = useFeedback()
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)
  const confirmAction = async (): Promise<void> => {
    setError('')
    setRunning(true)
    try {
      await request.action()
      if (request.successMessage)
        notify(request.successMessage)
      onClose()
    }
    catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setRunning(false)
    }
  }
  return <ConfirmDialog open message={request.message} busy={running} error={error} onClose={onClose} onConfirm={() => void confirmAction()} />
}

export function PageHeader({ title, actions }: { title: string, actions?: ReactNode }): React.JSX.Element {
  return <AdminPageHeader title={title} actions={actions} />
}

export function Token({ value }: { value: string }): React.JSX.Element {
  const [copied, setCopied] = useState(false)
  const { notify } = useFeedback()
  const copy = async (): Promise<void> => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1200)
    }
    catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not copy Client Token', 'error')
    }
  }
  return (
    <div className="token">
      <code>{value}</code>
      <IconButton label="Copy Client Token" onClick={() => void copy()}>{copied ? <Check size={15} /> : <Clipboard size={15} />}</IconButton>
    </div>
  )
}
