import * as Dialog from '@radix-ui/react-dialog'
import { Download } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../../shared/components/ui/button'

export function DownloadDialog({
  open,
  directoryPath,
  busy,
  serverError,
  onOpenChange,
  onInputChange,
  onSubmit,
}: {
  open: boolean
  directoryPath: string
  busy: boolean
  serverError?: string
  onOpenChange: (open: boolean) => void
  onInputChange: () => void
  onSubmit: (url: string, filename: string) => void
}): React.JSX.Element {
  const [url, setUrl] = useState('')
  const [filename, setFilename] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const error = serverError

  useEffect(() => {
    if (open) {
      setUrl('')
      setFilename('')
    }
  }, [open])

  const submit = (): void => {
    if (!url.trim() || busy)
      return
    onSubmit(url.trim(), filename.trim())
  }

  return (
    <Dialog.Root open={open} onOpenChange={next => !busy && onOpenChange(next)}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        {open && (
          <Dialog.Content
            className="confirm-dialog rename-dialog"
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
                <span className="rename-dialog-icon"><Download className="size-[18px]" /></span>
                <div className="min-w-0 flex-1">
                  <Dialog.Title className="text-base font-semibold">Download from URL</Dialog.Title>
                  <Dialog.Description className="mt-1 truncate text-xs text-muted-foreground" title={directoryPath}>{`Save to /${directoryPath}`}</Dialog.Description>
                </div>
              </div>
              <label className="rename-field">
                <span>Download URL</span>
                <input
                  ref={inputRef}
                  value={url}
                  aria-invalid={error !== undefined}
                  aria-describedby={error ? 'download-error' : undefined}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={busy}
                  onChange={(event) => {
                    setUrl(event.target.value)
                    onInputChange()
                  }}
                />
              </label>
              <label className="rename-field">
                <span>File name (optional)</span>
                <input
                  value={filename}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={busy}
                  onChange={(event) => {
                    setFilename(event.target.value)
                    onInputChange()
                  }}
                />
              </label>
              <div id="download-error" role={error ? 'alert' : undefined} className="rename-error">{error ?? ''}</div>
              <div className="mt-4 flex justify-end gap-2">
                <Dialog.Close asChild><Button disabled={busy}>Cancel</Button></Dialog.Close>
                <Button type="submit" className="rename-button" disabled={busy || !url.trim()}>{busy ? 'Starting...' : 'Start download'}</Button>
              </div>
            </form>
          </Dialog.Content>
        )}
      </Dialog.Portal>
    </Dialog.Root>
  )
}
