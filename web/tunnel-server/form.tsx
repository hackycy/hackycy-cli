import type { ReactNode } from 'react'

function issueMessage(error: unknown): string | undefined {
  if (!error || typeof error !== 'object')
    return undefined
  const value = error as { message?: unknown, root?: unknown }
  if (typeof value.message === 'string')
    return value.message
  return issueMessage(value.root)
}

export function FormField({ label, error, children }: { label: string, error?: unknown, children: ReactNode }): React.JSX.Element {
  return (
    <label>
      {label}
      {children}
      <FormMessage error={error} />
    </label>
  )
}

export function FormMessage({ error }: { error?: unknown }): React.JSX.Element | null {
  const message = issueMessage(error)
  if (!message)
    return null
  return <span className="field-error" role="alert">{message}</span>
}

export function FormError({ error }: { error?: unknown }): React.JSX.Element | null {
  const message = issueMessage(error)
  if (!message)
    return null
  return <p className="form-error" role="alert">{message}</p>
}
