import * as Dialog from '@radix-ui/react-dialog'
import { TriangleAlert } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../../../../shared/web/components/ui/button'
import { deleteConfirmationTarget, matchesDeleteConfirmation } from '../delete-confirmation'

export function DeleteDialog({
  paths,
  open,
  busy,
  onOpenChange,
  onConfirm,
}: {
  paths: string[]
  open: boolean
  busy: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}): React.JSX.Element {
  const [confirmation, setConfirmation] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const names = paths.map(path => path.split('/').at(-1) ?? path)
  const confirmationTarget = deleteConfirmationTarget(paths)
  const multiple = paths.length > 1
  const pathKey = paths.join('\0')
  const title = paths.length === 1
    ? `Delete "${confirmationTarget}" permanently?`
    : `Delete ${paths.length} items permanently?`
  const matches = matchesDeleteConfirmation(paths, confirmation)
  const mismatch = confirmation !== '' && !matches
  const inputLabel = multiple ? 'Type confirmation text to confirm' : 'Type item name to confirm'
  const mismatchMessage = multiple ? 'Confirmation text does not match.' : 'Name does not match.'

  useEffect(() => {
    setConfirmation('')
  }, [open, pathKey])

  const submit = (): void => {
    if (!busy && matches)
      onConfirm()
  }

  return (
    <Dialog.Root open={open} onOpenChange={next => !busy && onOpenChange(next)}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className="confirm-dialog delete-dialog"
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            inputRef.current?.focus()
          }}
        >
          <form
            onSubmit={(event) => {
              event.preventDefault()
              submit()
            }}
          >
            <div className="flex items-start gap-3">
              <span className="dialog-icon"><TriangleAlert className="size-5" /></span>
              <div className="min-w-0">
                <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
                <Dialog.Description className="mt-1 text-xs leading-5 text-muted-foreground">
                  This action cannot be undone.
                </Dialog.Description>
              </div>
            </div>
            {paths.length > 1 && (
              <div className="delete-list">
                {names.slice(0, 6).map((name, index) => <div key={`${paths[index]}:${index}`} className="truncate">{name}</div>)}
                {names.length > 6 && (
                  <div className="text-muted-foreground">
                    and
                    {names.length - 6}
                    {' '}
                    more
                  </div>
                )}
              </div>
            )}
            <label className="delete-confirmation-field">
              <span>
                Type
                {' '}
                <strong title={confirmationTarget}>{confirmationTarget}</strong>
                {' '}
                to confirm
              </span>
              <input
                ref={inputRef}
                value={confirmation}
                placeholder={confirmationTarget}
                aria-label={inputLabel}
                aria-invalid={mismatch}
                aria-describedby={mismatch ? 'delete-confirmation-error' : undefined}
                autoComplete="off"
                spellCheck={false}
                disabled={busy}
                onChange={event => setConfirmation(event.target.value)}
              />
            </label>
            <div id="delete-confirmation-error" role={mismatch ? 'alert' : undefined} className="delete-confirmation-error">{mismatch ? mismatchMessage : ''}</div>
            <div className="mt-4 flex justify-end gap-2">
              <Dialog.Close asChild><Button disabled={busy}>Cancel</Button></Dialog.Close>
              <Button type="submit" className="delete-button" disabled={busy || !matches}>{busy ? 'Deleting...' : 'Delete permanently'}</Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
