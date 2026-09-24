import type { FormEvent } from 'react'
import { Fingerprint, Plus, Search } from 'lucide-react'
import { useState } from 'react'
import { ApiError, apiJson, jsonRequest } from './api'
import { DialogShell, SegmentedControl } from './primitives'
import { Spinner } from './ui'

interface Preview {
  previewId: string
  managementAddress: string
  nodeFingerprint: string
  expiresAt: string
}

export function nodeActionError(cause: unknown): string {
  if (!(cause instanceof ApiError))
    return cause instanceof Error ? cause.message : String(cause)
  switch (cause.code) {
    case 'NODE_UNREACHABLE':
    case 'NODE_OFFLINE':
      return 'Check the management address and Node process. Existing bindings are unchanged.'
    case 'NODE_IDENTITY_MISMATCH':
      return 'Stop and compare the full fingerprint with the Node local output.'
    case 'NODE_ALREADY_CLAIMED':
      return 'This Node is already bound. Use Re-add only if it belongs to this Controller.'
    case 'NODE_PROTOCOL_INCOMPATIBLE':
      return 'Install the same supported release on Server and Node. Remote runtime is unknown.'
    case 'NODE_CLAIM_OUTCOME_UNKNOWN':
      return 'The binding may have succeeded. Preview again and use Re-add.'
    case 'NODE_PREVIEW_EXPIRED':
      return 'Preview the Node fingerprint again.'
    case 'REVISION_CONFLICT':
      return 'Configuration changed. Refresh and try again.'
    case 'NODE_CONFIG_REJECTED':
    case 'NODE_APPLY_FAILED':
    case 'NODE_ROLLBACK_FAILED':
      return 'Check the failed revision and phase, then save a corrected configuration.'
    default:
      return cause.message
  }
}

export function NodeClaimDialog({ onClose, onCreated }: { onClose: () => void, onCreated: (id: string) => void }): React.JSX.Element {
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [mode, setMode] = useState<'claim' | 'readd'>('claim')
  const [preview, setPreview] = useState<Preview>()
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      if (!preview) {
        const result = await apiJson<Preview>('/api/nodes/claim-previews', jsonRequest('POST', { managementAddress: address.trim() }))
        setPreview(result)
        setConfirmed(false)
      }
      else {
        if (!confirmed)
          return
        const result = await apiJson<{ node: { id: string } }>('/api/nodes', jsonRequest('POST', {
          previewId: preview.previewId,
          name: name.trim(),
          confirmedFingerprint: preview.nodeFingerprint,
          mode,
        }))
        onCreated(result.node.id)
      }
    }
    catch (cause) {
      setError(nodeActionError(cause))
      setPreview(undefined)
      setConfirmed(false)
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <DialogShell open title="Add Node" busy={busy} onOpenChange={open => !open && onClose()} onSubmit={event => void submit(event)}>
      <label>
        Display name
        <input required maxLength={100} value={name} disabled={busy} onChange={event => setName(event.target.value)} />
      </label>
      <label>
        Management address
        <input
          required
          type="url"
          placeholder="http://node.example.com:7600"
          value={address}
          disabled={busy}
          onChange={(event) => {
            setAddress(event.target.value)
            setPreview(undefined)
            setConfirmed(false)
          }}
        />
      </label>
      <SegmentedControl label="Registration mode" className="two-segments" value={mode} disabled={busy} onChange={value => setMode(value as 'claim' | 'readd')} options={[{ value: 'claim', label: 'Claim new Node' }, { value: 'readd', label: 'Re-add bound Node' }]} />
      {preview && (
        <div className="node-claim-preview">
          <div className="node-claim-fingerprint">
            <Fingerprint size={16} aria-hidden="true" />
            <code>{preview.nodeFingerprint}</code>
          </div>
          <label className="node-claim-confirm">
            <input className="import-checkbox" type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />
            Fingerprint matches the Node local output
          </label>
        </div>
      )}
      {error && <p className="runtime-error" role="alert">{error}</p>}
      <div className="modal-actions">
        <button type="button" onClick={onClose} disabled={busy}>Cancel</button>
        <button className="primary" type="submit" disabled={busy || Boolean(preview && !confirmed)}>
          {busy ? <Spinner /> : preview ? <Plus size={15} /> : <Search size={15} />}
          {preview ? mode === 'claim' ? 'Confirm claim' : 'Confirm re-add' : 'Preview fingerprint'}
        </button>
      </div>
    </DialogShell>
  )
}
