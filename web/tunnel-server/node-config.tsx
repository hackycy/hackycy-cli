import type { NodeManagementView } from './nodes-pages'
import { KeyRound, MapPin, RotateCw, Save, ShieldAlert, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { apiJson, jsonRequest } from './api'
import { nodeActionError } from './node-claim'
import { ConfirmDialog, DialogShell } from './primitives'
import { ErrorState, RowActionMenu, Spinner, useFeedback } from './ui'

export function NodeActions({ node, onSaved, onForgotten }: { node: NodeManagementView, onSaved: () => void, onForgotten: () => void }): React.JSX.Element | null {
  const [activeAction, setActiveAction] = useState<'details' | 'rotate' | 'remove' | 'forget' | null>(null)
  const [reapplying, setReapplying] = useState(false)
  const { notify } = useFeedback()
  const remote = node.kind === 'remote'
  const running = remote && node.lifecycle === 'active' && node.desired.mode === 'running'
  const canForget = remote && (node.lifecycle === 'removing' || ['unreachable', 'identity_mismatch', 'incompatible'].includes(node.management.state))
  if (!remote)
    return null
  const reapply = async (): Promise<void> => {
    setReapplying(true)
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/reapply`, { method: 'POST' })
      notify('Node re-apply queued')
      onSaved()
    }
    catch (cause) {
      notify(nodeActionError(cause), 'error')
    }
    finally {
      setReapplying(false)
    }
  }
  return (
    <>
      {remote && node.lifecycle === 'active' && <NodeConfigurationEditor node={node} onSaved={onSaved} />}
      <RowActionMenu
        label="More Node actions"
        actions={[
          { label: 'Edit details', icon: MapPin, onSelect: () => setActiveAction('details') },
          ...(running
            ? [
                { label: 'Re-apply snapshot', icon: RotateCw, disabled: reapplying, onSelect: () => void reapply() },
                { label: 'Rotate FRP Token', icon: KeyRound, disabled: node.tokenRotation.state !== 'idle', onSelect: () => setActiveAction('rotate') },
              ]
            : []),
          ...(remote && node.lifecycle === 'active' ? [{ label: 'Remove Node', icon: Trash2, destructive: true, onSelect: () => setActiveAction('remove') }] : []),
          ...(canForget ? [{ label: 'Force Forget', icon: ShieldAlert, destructive: true, onSelect: () => setActiveAction('forget') }] : []),
        ]}
      />
      {activeAction === 'details' && <NodeMetadataEditor node={node} onSaved={onSaved} autoOpen onClose={() => setActiveAction(null)} />}
      {activeAction === 'rotate' && <NodeTokenRotationButton node={node} onSaved={onSaved} autoOpen onClose={() => setActiveAction(null)} />}
      {(activeAction === 'remove' || activeAction === 'forget') && <NodeRemovalControls node={node} onSaved={onSaved} onForgotten={onForgotten} initialAction={activeAction} onClose={() => setActiveAction(null)} />}
    </>
  )
}

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
  if (node.kind === 'local' || node.lifecycle === 'removing')
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
      <button className="primary" type="button" onClick={() => setOpen(true)}>
        <Save size={15} />
        Edit configuration
      </button>
      <DialogShell
        open={open}
        title="Edit Node configuration"
        className="node-modal"
        busy={saving}
        onOpenChange={next => !next && setOpen(false)}
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <div className="node-form-section">
          <h3>FRPS listener</h3>
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
          </div>
        </div>
        <div className="node-form-section">
          <h3>Port allocation</h3>
          <div className="form-grid form-grid-two">
            <label>
              Port pool start
              <input required type="number" min={1} max={65535} value={poolStart} onChange={event => setPoolStart(event.target.value)} />
            </label>
            <label>
              Port pool end
              <input required type="number" min={1} max={65535} value={poolEnd} onChange={event => setPoolEnd(event.target.value)} />
            </label>
          </div>
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

export function NodeTokenRotationButton({ node, onSaved, autoOpen = false, onClose }: { node: NodeManagementView, onSaved: () => void, autoOpen?: boolean, onClose?: () => void }): React.JSX.Element | null {
  const [open, setOpen] = useState(autoOpen)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  if (node.kind === 'local' || node.lifecycle === 'removing' || node.desired.mode !== 'running')
    return null
  const close = (): void => {
    setOpen(false)
    onClose?.()
  }
  const rotate = async (): Promise<void> => {
    setBusy(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/token-rotation`, jsonRequest('POST', { expectedRevision: node.desired.revision }))
      close()
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
      {!autoOpen && (
        <button type="button" disabled={node.tokenRotation.state !== 'idle'} onClick={() => setOpen(true)}>
          <KeyRound size={15} />
          Rotate FRP Token
        </button>
      )}
      <ConfirmDialog
        open={open}
        message="Assigned Clients may briefly disconnect while this Node applies its new shared FRP Token. Continue?"
        busy={busy}
        error={error}
        onClose={close}
        onConfirm={() => void rotate()}
      />
    </>
  )
}

export function NodeRemovalControls({ node, onSaved, onForgotten, initialAction, onClose }: { node: NodeManagementView, onSaved: () => void, onForgotten: () => void, initialAction?: 'remove' | 'forget', onClose?: () => void }): React.JSX.Element | null {
  const [removeOpen, setRemoveOpen] = useState(initialAction === 'remove')
  const [forgetOpen, setForgetOpen] = useState(initialAction === 'forget')
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  if (node.kind === 'local')
    return null

  const close = (): void => {
    setRemoveOpen(false)
    setForgetOpen(false)
    onClose?.()
  }

  const canForget = node.lifecycle === 'removing' || ['unreachable', 'identity_mismatch', 'incompatible'].includes(node.management.state)
  const remove = async (): Promise<void> => {
    setBusy(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}`, { method: 'DELETE' })
      close()
      notify('Node removal pending remote shutdown')
      onSaved()
    }
    catch (cause) {
      setError(nodeActionError(cause))
    }
    finally {
      setBusy(false)
    }
  }
  const forget = async (): Promise<void> => {
    setBusy(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/force-forget`, jsonRequest('POST', { confirmNodeId: confirmation.trim() }))
      close()
      notify('Server Node record removed')
      onForgotten()
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
      {!initialAction && node.lifecycle === 'active' && (
        <button
          type="button"
          onClick={() => {
            setError('')
            setRemoveOpen(true)
          }}
        >
          <Trash2 size={15} />
          Remove Node
        </button>
      )}
      {!initialAction && canForget && (
        <button
          className="danger"
          type="button"
          onClick={() => {
            setError('')
            setConfirmation('')
            setForgetOpen(true)
          }}
        >
          <ShieldAlert size={15} />
          Force Forget
        </button>
      )}
      <ConfirmDialog
        open={removeOpen}
        message="Remove this Node after it has no assigned or pending Clients? The Node stays listed until it confirms durable shutdown of FRPS. If it is offline, its old FRPS may still be running."
        busy={busy}
        error={error}
        onClose={close}
        onConfirm={() => void remove()}
      />
      <DialogShell
        open={forgetOpen}
        title="Force Forget Node"
        className="node-modal"
        busy={busy}
        onOpenChange={next => !next && close()}
        onSubmit={(event) => {
          event.preventDefault()
          void forget()
        }}
      >
        <p>Only the Server record will be deleted. The remote FRPS may continue running, old credentials may still work, and the Node remains bound to this Controller.</p>
        <label>
          Enter the full Node ID to confirm
          <span className="mono break">{node.id}</span>
          <input required autoComplete="off" spellCheck={false} value={confirmation} onChange={event => setConfirmation(event.target.value)} />
        </label>
        {error && <ErrorState message={error} />}
        <div className="modal-actions">
          <button type="button" disabled={busy} onClick={close}>Cancel</button>
          <button className="danger" type="submit" disabled={busy || confirmation.trim() !== node.id}>
            {busy ? <Spinner /> : <ShieldAlert size={15} />}
            Force Forget
          </button>
        </div>
      </DialogShell>
    </>
  )
}

export function NodeMetadataEditor({ node, onSaved, autoOpen = false, onClose }: { node: NodeManagementView, onSaved: () => void, autoOpen?: boolean, onClose?: () => void }): React.JSX.Element | null {
  const [open, setOpen] = useState(autoOpen)
  const [name, setName] = useState(node.name)
  const [managementAddress, setManagementAddress] = useState(node.managementAddress ?? '')
  const [frpHost, setFrpHost] = useState(node.advertisedFrpAddress?.host ?? '')
  const [frpPort, setFrpPort] = useState(String(node.advertisedFrpAddress?.port ?? ''))
  const [httpHost, setHttpHost] = useState(node.httpIngressAddress?.host ?? '')
  const [httpPort, setHttpPort] = useState(String(node.httpIngressAddress?.port ?? ''))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  const close = (): void => {
    setOpen(false)
    onClose?.()
  }
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
      close()
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
      {!autoOpen && (
        <button type="button" onClick={() => setOpen(true)}>
          <MapPin size={15} />
          Edit details
        </button>
      )}
      <DialogShell
        open={open}
        title="Edit Node details"
        className="node-modal"
        busy={saving}
        onOpenChange={next => !next && close()}
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <div className="node-form-section">
          <h3>Identity & management</h3>
          <label>
            Display name
            <input required maxLength={100} value={name} onChange={event => setName(event.target.value)} />
          </label>
          <label>
            Management address
            <input required type="url" value={managementAddress} onChange={event => setManagementAddress(event.target.value)} />
          </label>
        </div>
        <div className="node-form-section">
          <h3>Public endpoints</h3>
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
        </div>
        {node.pendingManagementAddress && (
          <p className="form-hint">
            Pending management address:
            {' '}
            <span className="mono break">{node.pendingManagementAddress}</span>
            . It becomes active only after the original Node identity is verified.
          </p>
        )}
        {error && <ErrorState message={error} />}
        <div className="modal-actions">
          <button type="button" disabled={saving} onClick={close}>Cancel</button>
          <button className="primary" type="submit" disabled={saving}>
            {saving ? <Spinner /> : <Save size={15} />}
            Save details
          </button>
        </div>
      </DialogShell>
    </>
  )
}
