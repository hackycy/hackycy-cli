import type { DirectoryEntry } from '../api'
import * as Dialog from '@radix-ui/react-dialog'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../../../../shared/web/components/ui/button'
import { entryNameError, renameSelectionEnd } from '../explorer-state'
import { EntryGlyph } from './entry-glyph'

export function RenameDialog({
  entry,
  busy,
  serverError,
  onOpenChange,
  onNameChange,
  onConfirm,
}: {
  entry?: DirectoryEntry
  busy: boolean
  serverError?: string
  onOpenChange: (open: boolean) => void
  onNameChange: () => void
  onConfirm: (name: string) => void
}): React.JSX.Element {
  const [name, setName] = useState(entry?.name ?? '')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (entry)
      setName(entry.name)
  }, [entry?.name, entry?.path])

  const validationError = entryNameError(name)
  const unchanged = entry === undefined || name === entry.name
  const error = validationError ?? serverError
  const submit = (): void => {
    if (!entry || busy || validationError || unchanged)
      return
    onConfirm(name)
  }

  return (
    <Dialog.Root open={entry !== undefined} onOpenChange={next => !busy && onOpenChange(next)}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        {entry && (
          <Dialog.Content
            className="confirm-dialog rename-dialog"
            onOpenAutoFocus={(event) => {
              event.preventDefault()
              inputRef.current?.focus()
              inputRef.current?.setSelectionRange(0, renameSelectionEnd(entry))
            }}
          >
            <form
              onSubmit={(event) => {
                event.preventDefault()
                submit()
              }}
            >
              <div className="flex items-start gap-3">
                <span className="rename-dialog-icon"><EntryGlyph entry={entry} className="size-[18px]" /></span>
                <div className="min-w-0 flex-1">
                  <Dialog.Title className="text-base font-semibold">Rename item</Dialog.Title>
                  <Dialog.Description className="mt-1 truncate text-xs text-muted-foreground">{entry.name}</Dialog.Description>
                </div>
              </div>
              <label className="rename-field">
                <span>New name</span>
                <input
                  ref={inputRef}
                  value={name}
                  aria-invalid={error !== undefined}
                  aria-describedby={error ? 'rename-error' : undefined}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={busy}
                  onChange={(event) => {
                    setName(event.target.value)
                    onNameChange()
                  }}
                />
              </label>
              <div id="rename-error" role={error ? 'alert' : undefined} className="rename-error">{error ?? ''}</div>
              <div className="mt-4 flex justify-end gap-2">
                <Dialog.Close asChild><Button disabled={busy}>Cancel</Button></Dialog.Close>
                <Button type="submit" className="rename-button" disabled={busy || validationError !== undefined || unchanged}>{busy ? 'Renaming...' : 'Rename'}</Button>
              </div>
            </form>
          </Dialog.Content>
        )}
      </Dialog.Portal>
    </Dialog.Root>
  )
}
