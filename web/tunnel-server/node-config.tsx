import type { NodeManagementView } from './nodes-pages'
import { KeyRound, MapPin, RotateCw, Save } from 'lucide-react'
import { useEffect, useState } from 'react'
import { apiJson, jsonRequest } from './api'
import { nodeActionError } from './node-claim'
import { ConfirmDialog, DialogShell } from './primitives'
import { ErrorState, Spinner, useFeedback } from './ui'

export function NodeConfigurationEditor({ node, onSaved }: { node: NodeManagementView, onSaved: () => void }): React.JSX.Element | null {
  const [open, setOpen] = useState(false)
  const [bindAddress, setBindAddress] = useState('127.0.0.1')
  const [bindPort, setBindPort] = useState('7000')
  const [vhostPort, setVhostPort] = useState('8080')
  const [poolStart, setPoolStart] = useState('20000')
  const [poolEnd, setPoolEnd] = useState('29999')
  const [custom404Page, setCustom404Page] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const { notify } = useFeedback()
  useEffect(() => {
    const settings = node.desired.settings
    if (!settings)
      return
    setBindAddress(settings.bindAddress)
    setBindPort(String(settings.bindPort))
    setVhostPort(String(settings.vhostHTTPPort))
    setPoolStart(String(settings.portRangeStart))
    setPoolEnd(String(settings.portRangeEnd))
    setCustom404Page(settings.custom404Page)
  }, [node])
  if (node.kind === 'local')
    return null
  const save = async (): Promise<void> => {
    setSaving(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/desired`, jsonRequest('PUT', {
        expectedRevision: node.desired.revision,
        settings: {
          bindAddress,
          bindPort: Number(bindPort),
          vhostHTTPPort: Number(vhostPort),
          portRangeStart: Number(poolStart),
          portRangeEnd: Number(poolEnd),
          custom404Page,
        },
      }))
      setOpen(false)
      notify('Node configuration saved')
      onSaved()
    }
    catch (cause) {
      setError(nodeActionError(cause))
    }
    finally {
      setSaving(false)
    }
  }
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        <Save size={15} />
        Edit configuration
      </button>
      <DialogShell
        open={open}
        title="Edit Node configuration"
        busy={saving}
        onOpenChange={next => !next && setOpen(false)}
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <div className="form-grid form-grid-two">
          <label>
            Bind address
            <input required value={bindAddress} onChange={event => setBindAddress(event.target.value)} />
          </label>
          <label>
            Bind port
            <input required type="number" min={1} max={65535} value={bindPort} onChange={event => setBindPort(event.target.value)} />
          </label>
          <label>
            HTTP vhost port
            <input required type="number" min={1} max={65535} value={vhostPort} onChange={event => setVhostPort(event.target.value)} />
          </label>
          <label>
            Port pool start
            <input required type="number" min={1} max={65535} value={poolStart} onChange={event => setPoolStart(event.target.value)} />
          </label>
          <label>
            Port pool end
            <input required type="number" min={1} max={65535} value={poolEnd} onChange={event => setPoolEnd(event.target.value)} />
          </label>
        </div>
        <label>
          Custom 404 page
          <textarea maxLength={524288} value={custom404Page} onChange={event => setCustom404Page(event.target.value)} />
        </label>
        {error && <ErrorState message={error} />}
        <div className="modal-actions">
          <button type="button" disabled={saving} onClick={() => setOpen(false)}>Cancel</button>
          <button className="primary" type="submit" disabled={saving}>
            {saving ? <Spinner /> : <Save size={15} />}
            Save and apply
          </button>
        </div>
      </DialogShell>
    </>
  )
}

export function NodeReapplyButton({ node, onSaved }: { node: NodeManagementView, onSaved: () => void }): React.JSX.Element | null {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  if (node.kind === 'local' || node.desired.mode !== 'running')
    return null
  const reapply = async (): Promise<void> => {
    setBusy(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/reapply`, { method: 'POST' })
      notify('Node re-apply queued')
      onSaved()
    }
    catch (cause) {
      setError(nodeActionError(cause))
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <div className="node-action-stack">
      <button type="button" disabled={busy} onClick={() => void reapply()}>
        {busy ? <Spinner /> : <RotateCw size={15} />}
        Re-apply snapshot
      </button>
      {error && <small className="runtime-error">{error}</small>}
    </div>
  )
}

export function NodeTokenRotationButton({ node, onSaved }: { node: NodeManagementView, onSaved: () => void }): React.JSX.Element | null {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  if (node.kind === 'local' || node.desired.mode !== 'running')
    return null
  const rotate = async (): Promise<void> => {
    setBusy(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/token-rotation`, jsonRequest('POST', { expectedRevision: node.desired.revision }))
      setOpen(false)
      notify('Node Token rotation queued')
      onSaved()
    }
    catch (cause) {
      setError(nodeActionError(cause))
    }
    finally {
      setBusy(false)
    }
  }
  return (
    <>
      <button type="button" disabled={node.tokenRotation.state !== 'idle'} onClick={() => setOpen(true)}>
        <KeyRound size={15} />
        Rotate FRP Token
      </button>
      <ConfirmDialog
        open={open}
        message="Assigned Clients may briefly disconnect while this Node applies its new shared FRP Token. Continue?"
        busy={busy}
        error={error}
        onClose={() => setOpen(false)}
        onConfirm={() => void rotate()}
      />
    </>
  )
}

export function NodeMetadataEditor({ node, onSaved }: { node: NodeManagementView, onSaved: () => void }): React.JSX.Element | null {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(node.name)
  const [managementAddress, setManagementAddress] = useState(node.managementAddress ?? '')
  const [frpHost, setFrpHost] = useState(node.advertisedFrpAddress?.host ?? '')
  const [frpPort, setFrpPort] = useState(String(node.advertisedFrpAddress?.port ?? ''))
  const [httpHost, setHttpHost] = useState(node.httpIngressAddress?.host ?? '')
  const [httpPort, setHttpPort] = useState(String(node.httpIngressAddress?.port ?? ''))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  const save = async (): Promise<void> => {
    setSaving(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}`, jsonRequest('PATCH', {
        name: name.trim(),
        managementAddress: managementAddress.trim(),
        advertisedFrpAddress: frpHost.trim() ? { host: frpHost.trim(), port: Number(frpPort) } : undefined,
        httpIngressAddress: httpHost.trim() ? { host: httpHost.trim(), port: Number(httpPort) } : undefined,
      }))
      setOpen(false)
      notify('Node details saved')
      onSaved()
    }
    catch (cause) {
      setError(nodeActionError(cause))
    }
    finally {
      setSaving(false)
    }
  }
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        <MapPin size={15} />
        Edit details
      </button>
      <DialogShell
        open={open}
        title="Edit Node details"
        busy={saving}
        onOpenChange={next => !next && setOpen(false)}
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <label>
          Display name
          <input required maxLength={100} value={name} onChange={event => setName(event.target.value)} />
        </label>
        <label>
          Management address
          <input required type="url" value={managementAddress} onChange={event => setManagementAddress(event.target.value)} />
        </label>
        <div className="form-grid form-grid-two">
          <label>
            FRP host
            <input value={frpHost} onChange={event => setFrpHost(event.target.value)} />
          </label>
          <label>
            FRP port
            <input type="number" min={1} max={65535} value={frpPort} onChange={event => setFrpPort(event.target.value)} />
          </label>
          <label>
            HTTP ingress host
            <input value={httpHost} onChange={event => setHttpHost(event.target.value)} />
          </label>
          <label>
            HTTP ingress port
            <input type="number" min={1} max={65535} value={httpPort} onChange={event => setHttpPort(event.target.value)} />
          </label>
        </div>
        {node.pendingManagementAddress && (
          <p className="form-hint">
            Pending management address:
            <span className="mono">{node.pendingManagementAddress}</span>
            . It becomes active only after the original Node identity is verified.
          </p>
        )}
        {error && <ErrorState message={error} />}
        <div className="modal-actions">
          <button type="button" disabled={saving} onClick={() => setOpen(false)}>Cancel</button>
          <button className="primary" type="submit" disabled={saving}>
            {saving ? <Spinner /> : <Save size={15} />}
            Save details
          </button>
        </div>
      </DialogShell>
    </>
  )
}
