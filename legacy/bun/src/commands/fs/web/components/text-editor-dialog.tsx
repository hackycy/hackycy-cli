import type { DirectoryEntry, TextPreview, TextSaveResult } from '../api'
import * as Dialog from '@radix-ui/react-dialog'
import { AlertTriangle, Download, LoaderCircle, Save, X } from 'lucide-react'
import * as monaco from 'monaco-editor'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../../../../shared/web/components/ui/button'
import { ApiError, apiJson } from '../api'
import { formatFileSize } from './file-browser'

export type ReadyTextPreview = Extract<TextPreview, { status: 'ready' }>

export interface TextEditorTarget {
  entry: DirectoryEntry
  preview: ReadyTextPreview
}

export function TextEditorDialog({ target, theme, onDirtyChange, onSaved, onClose }: {
  target?: TextEditorTarget
  theme: 'light' | 'dark'
  onDirtyChange: (dirty: boolean) => void
  onSaved: () => void
  onClose: () => void
}): React.JSX.Element {
  const [remote, setRemote] = useState<ReadyTextPreview>()
  const [draft, setDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [conflict, setConflict] = useState<string>()
  const dirty = target !== undefined && remote !== undefined && draft !== remote.text

  useEffect(() => {
    if (!target) {
      setRemote(undefined)
      setDraft('')
      setConflict(undefined)
      setSaving(false)
      return
    }
    setRemote(target.preview)
    setDraft(target.preview.text)
    setConflict(undefined)
  }, [target?.entry.path, target?.preview.revision])

  useEffect(() => {
    onDirtyChange(Boolean(dirty))
    return () => onDirtyChange(false)
  }, [dirty, onDirtyChange])

  useEffect(() => {
    if (!dirty)
      return
    const beforeUnload = (event: BeforeUnloadEvent): void => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [dirty])

  const close = (): void => {
    if (saving)
      return
    // eslint-disable-next-line no-alert
    if (dirty && !window.confirm('Discard unsaved changes?'))
      return
    onClose()
  }

  const save = async (): Promise<void> => {
    if (!target || !remote || saving || !dirty)
      return
    setSaving(true)
    setConflict(undefined)
    try {
      const result = await apiJson<TextSaveResult>(`/api/text?path=${encodeURIComponent(target.entry.path)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain; charset=utf-8', 'If-Match': remote.revision },
        body: draft,
      })
      setRemote({ version: 1, status: 'ready', text: draft, encoding: result.encoding, size: result.size, revision: result.revision })
      setConflict(undefined)
      onSaved()
    }
    catch (cause) {
      if (cause instanceof ApiError && cause.status === 412)
        setConflict('The file changed on the server. Your draft is still open.')
      else
        setConflict(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setSaving(false)
    }
  }

  const reloadRemote = async (): Promise<void> => {
    if (!target || saving)
      return
    try {
      const next = await apiJson<TextPreview>(`/api/text?path=${encodeURIComponent(target.entry.path)}`)
      if (next.status === 'ready') {
        setRemote(next)
        setDraft(next.text)
        setConflict(undefined)
      }
      else {
        setConflict(next.status === 'binary' ? 'The remote file is no longer text.' : 'The remote file is now too large to edit.')
      }
    }
    catch (cause) {
      setConflict(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const downloadDraft = (): void => {
    if (!target)
      return
    const url = URL.createObjectURL(new Blob([draft], { type: 'text/plain;charset=utf-8' }))
    const link = document.createElement('a')
    link.href = url
    link.download = `${target.entry.name}.draft`
    link.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Dialog.Root open={target !== undefined} onOpenChange={open => !open && close()}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay text-editor-dialog-overlay" />
        {target && remote && (
          <Dialog.Content
            className="text-editor-dialog"
            onEscapeKeyDown={(event) => {
              event.preventDefault()
              close()
            }}
            onPointerDownOutside={(event) => {
              event.preventDefault()
              close()
            }}
            onOpenAutoFocus={event => event.preventDefault()}
          >
            <Dialog.Title className="sr-only">
              Edit
              {' '}
              {target.entry.name}
            </Dialog.Title>
            <Dialog.Description className="sr-only">Edit the text file and save changes</Dialog.Description>
            <header className="text-editor-dialog-header">
              <div className="min-w-0 flex-1">
                <h2 className="truncate text-sm font-semibold" title={target.entry.name}>{target.entry.name}</h2>
                <p className="truncate text-xs text-muted-foreground">{`${target.entry.mimeType ?? 'Unknown type'} · ${formatFileSize(remote.size)} · ${remote.encoding.toUpperCase()}`}</p>
              </div>
              <Button variant="ghost" size="icon" title="Save file" aria-label="Save file" disabled={saving || !dirty} onClick={() => void save()}>{saving ? <LoaderCircle className="size-4 animate-spin" /> : <Save className="size-4" />}</Button>
              <Button variant="ghost" size="icon" title="Close editor" aria-label="Close editor" disabled={saving} onClick={close}><X className="size-4" /></Button>
            </header>
            {conflict && (
              <div className="text-editor-conflict" role="alert">
                <AlertTriangle className="size-4 shrink-0" />
                <span>{conflict}</span>
                {conflict.startsWith('The file changed') && <Button variant="outline" size="default" onClick={() => void reloadRemote()}>Reload remote</Button>}
                {conflict.startsWith('The file changed') && (
                  <Button variant="outline" size="default" onClick={downloadDraft}>
                    <Download className="mr-1.5 size-3.5" />
                    Download draft
                  </Button>
                )}
              </div>
            )}
            <div className="text-editor-dialog-body">
              <MonacoEditor value={draft} language={target.entry.syntaxLanguage} theme={theme} onChange={setDraft} onSave={() => void save()} />
            </div>
          </Dialog.Content>
        )}
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function monacoLanguage(language: string | undefined): string {
  switch (language) {
    case 'javascript': case 'jsx': return 'javascript'
    case 'typescript': case 'tsx': return 'typescript'
    case 'json': return 'json'
    case 'html': case 'xml': case 'svg': return 'html'
    case 'css': case 'scss': case 'less': return language
    case 'markdown': return 'markdown'
    case 'python': return 'python'
    case 'yaml': return 'yaml'
    default: return 'plaintext'
  }
}

function MonacoEditor({ value, language, theme, onChange, onSave }: { value: string, language?: string, theme: 'light' | 'dark', onChange: (value: string) => void, onSave: () => void }): React.JSX.Element {
  const hostRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null)
  const modelRef = useRef<monaco.editor.ITextModel | null>(null)
  const suppressChangeRef = useRef(false)
  const callbacks = useRef({ onChange, onSave })
  callbacks.current = { onChange, onSave }

  useEffect(() => {
    const parent = hostRef.current
    if (!parent)
      return
    const model = monaco.editor.createModel(value, monacoLanguage(language))
    const editor = monaco.editor.create(parent, {
      model,
      automaticLayout: true,
      bracketPairColorization: { enabled: true },
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      largeFileOptimizations: true,
      lineNumbers: 'on',
      minimap: { enabled: false },
      padding: { top: 8, bottom: 8 },
      scrollBeyondLastLine: false,
      scrollbar: { alwaysConsumeMouseWheel: false, verticalScrollbarSize: 8, horizontalScrollbarSize: 8 },
      wordWrap: 'on',
    })
    monaco.editor.setTheme(theme === 'dark' ? 'vs-dark' : 'vs')
    const changeSubscription = model.onDidChangeContent(() => {
      if (!suppressChangeRef.current)
        callbacks.current.onChange(model.getValue())
    })
    editor.addAction({
      id: 'fs.save-text-file',
      label: 'Save file',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS],
      run: () => {
        callbacks.current.onSave()
      },
    })
    editorRef.current = editor
    modelRef.current = model
    editor.focus()
    return () => {
      changeSubscription.dispose()
      editor.dispose()
      model.dispose()
      editorRef.current = null
      modelRef.current = null
    }
  }, [language, theme])

  useEffect(() => {
    const model = modelRef.current
    if (model && model.getValue() !== value) {
      suppressChangeRef.current = true
      model.setValue(value)
      suppressChangeRef.current = false
    }
  }, [value])

  return <div ref={hostRef} role="textbox" aria-label="Text editor" className="text-editor-surface" />
}
