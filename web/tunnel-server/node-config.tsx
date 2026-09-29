import type { NodeManagementView } from './nodes-pages'
import { KeyRound, Pencil, RotateCw, Save, ShieldAlert, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { apiJson, jsonRequest } from './api'
import { nodeActionError } from './node-claim'
import { ConfirmDialog, DialogShell, SegmentedControl } from './primitives'
import { ErrorState, RowActionMenu, Spinner, Switch, useFeedback } from './ui'

type Custom404PageMode = 'inherit' | 'custom' | 'default'

export function Node404PageSource({ mode, content, effectiveContent, onModeChange, onContentChange }: { mode: Custom404PageMode, content: string, effectiveContent?: string, onModeChange: (mode: Custom404PageMode) => void, onContentChange: (content: string) => void }): React.JSX.Element {
  return (
    <div className="node-404-setting">
      <span>Custom 404 page</span>
      <SegmentedControl
        label="Custom 404 page source"
        value={mode}
        onChange={value => onModeChange(value as Custom404PageMode)}
        options={[{ value: 'inherit', label: 'Inherit Server' }, { value: 'custom', label: 'Custom' }, { value: 'default', label: 'FRP default' }]}
      />
      {mode === 'custom' && <textarea aria-label="Custom 404 page HTML" maxLength={524288} value={content} onChange={event => onContentChange(event.target.value)} />}
      {mode === 'inherit' && <p className="form-hint">{effectiveContent ? 'Using the Server custom 404 page.' : 'The Server has no custom 404 page; FRP default is active.'}</p>}
    </div>
  )
}

export function NodeActions({ node, onSaved, onForgotten }: { node: NodeManagementView, onSaved: () => void, onForgotten: () => void }): React.JSX.Element | null {
  const [activeAction, setActiveAction] = useState<'name' | 'rotate' | 'remove' | 'forget' | null>(null)
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
      <RowActionMenu
        label="More Node actions"
        actions={[
          { label: 'Rename Node', icon: Pencil, onSelect: () => setActiveAction('name') },
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
      {activeAction === 'name' && <NodeMetadataEditor node={node} mode="name" onSaved={onSaved} onClose={() => setActiveAction(null)} />}
      {activeAction === 'rotate' && <NodeTokenRotationButton node={node} onSaved={onSaved} autoOpen onClose={() => setActiveAction(null)} />}
      {(activeAction === 'remove' || activeAction === 'forget') && <NodeRemovalControls node={node} onSaved={onSaved} onForgotten={onForgotten} initialAction={activeAction} onClose={() => setActiveAction(null)} />}
    </>
  )
}

export function NodeConfigurationEditor({ node, onSaved, onClose }: { node: NodeManagementView, onSaved: () => void, onClose: () => void }): React.JSX.Element | null {
  const [bindAddress, setBindAddress] = useState('0.0.0.0')
  const [bindPort, setBindPort] = useState('7000')
  const [vhostPort, setVhostPort] = useState('8080')
  const [poolStart, setPoolStart] = useState('20000')
  const [poolEnd, setPoolEnd] = useState('20100')
  const [custom404Page, setCustom404Page] = useState('')
  const [custom404PageMode, setCustom404PageMode] = useState<Custom404PageMode>('inherit')
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
    setCustom404PageMode(settings.custom404PageMode ?? (settings.custom404Page ? 'custom' : 'inherit'))
  }, [node])
  if (node.kind === 'local' || node.lifecycle === 'removing')
    return null
  const save = async (): Promise<void> => {
    const frpPort = Number(bindPort)
    const httpPort = Number(vhostPort)
    const firstPort = Number(poolStart)
    const lastPort = Number(poolEnd)
    if (firstPort > lastPort || frpPort === httpPort || [frpPort, httpPort].some(port => port >= firstPort && port <= lastPort)) {
      setError('Use separate FRP and HTTP ports outside the port pool, with the pool start no higher than its end.')
      return
    }
    setSaving(true)
    setError('')
    try {
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}/desired`, jsonRequest('PUT', {
        expectedRevision: node.desired.revision,
        settings: {
          bindAddress,
          bindPort: frpPort,
          vhostHTTPPort: httpPort,
          portRangeStart: firstPort,
          portRangeEnd: lastPort,
          custom404Page: custom404PageMode === 'custom' ? custom404Page : '',
          custom404PageMode,
        },
      }))
      onClose()
      notify('FRPS settings saved on Server; waiting for Node application')
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
    <DialogShell
      open
      title="FRPS listener & port pool"
      className="node-modal"
      busy={saving}
      onOpenChange={next => !next && onClose()}
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <div className="node-form-section">
        <h3>FRPS listener</h3>
        <p className="form-hint">Bind address is inside the Node container. Use 0.0.0.0 for Docker unless you need a narrower interface; do not enter the public IP here.</p>
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
        <p className="form-hint">Publish the FRP and HTTP ports plus this entire pool from Docker. Publish the pool for both TCP and UDP.</p>
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
      <details className="node-advanced-settings">
        <summary>Advanced settings</summary>
        <Node404PageSource mode={custom404PageMode} content={custom404Page} effectiveContent={node.desired.effectiveCustom404Page} onModeChange={setCustom404PageMode} onContentChange={setCustom404Page} />
      </details>
      {error && <ErrorState message={error} />}
      <div className="modal-actions">
        <button type="button" disabled={saving} onClick={onClose}>Cancel</button>
        <button className="primary" type="submit" disabled={saving}>
          {saving ? <Spinner /> : <Save size={15} />}
          Save settings
        </button>
      </div>
    </DialogShell>
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

export function NodeMetadataEditor({ node, mode, onSaved, onClose }: { node: NodeManagementView, mode: 'name' | 'management' | 'endpoints', onSaved: () => void, onClose: () => void }): React.JSX.Element | null {
  const [name, setName] = useState(node.name)
  const [managementAddress, setManagementAddress] = useState(node.managementAddress ?? '')
  const [frpHost, setFrpHost] = useState(node.advertisedFrpAddress?.host ?? '')
  const [frpPort, setFrpPort] = useState(String(node.advertisedFrpAddress?.port ?? ''))
  const [httpEnabled, setHTTPEnabled] = useState(Boolean(node.httpIngressAddress))
  const [httpHost, setHttpHost] = useState(node.httpIngressAddress?.host ?? '')
  const [httpPort, setHttpPort] = useState(String(node.httpIngressAddress?.port ?? ''))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  const save = async (): Promise<void> => {
    setSaving(true)
    setError('')
    try {
      const patch = mode === 'name'
        ? { name: name.trim() }
        : mode === 'management'
          ? { managementAddress: managementAddress.trim() }
          : {
              advertisedFrpAddress: { host: frpHost.trim(), port: Number(frpPort) },
              httpIngressAddress: httpEnabled ? { host: httpHost.trim(), port: Number(httpPort) } : undefined,
              clearHttpIngressAddress: !httpEnabled && Boolean(node.httpIngressAddress),
            }
      await apiJson(`/api/nodes/${encodeURIComponent(node.id)}`, jsonRequest('PATCH', patch))
      onClose()
      notify(mode === 'endpoints' ? 'Public endpoints saved on Server' : mode === 'management' ? 'Management address saved for verification' : 'Node renamed')
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
    <DialogShell
      open
      title={mode === 'name' ? 'Rename Node' : mode === 'management' ? 'Management connection' : 'Public endpoints'}
      className="node-modal"
      busy={saving}
      onOpenChange={next => !next && onClose()}
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      {mode === 'name' && (
        <label>
          Display name
          <input required maxLength={100} value={name} onChange={event => setName(event.target.value)} />
        </label>
      )}
      {mode === 'management' && (
        <div className="node-form-section">
          <p className="form-hint">The Server uses this URL to reach the Node management port and verify its identity.</p>
          <label>
            Management address
            <input required type="url" value={managementAddress} onChange={event => setManagementAddress(event.target.value)} />
          </label>
          {node.pendingManagementAddress && (
            <p className="form-hint">
              Pending address:
              {' '}
              <span className="mono break">{node.pendingManagementAddress}</span>
              . It becomes active after the original Node identity is verified.
            </p>
          )}
        </div>
      )}
      {mode === 'endpoints' && (
        <div className="node-form-section">
          <p className="form-hint">Clients use the public FRP address. It can differ from the container bind address and port; saving it does not verify external reachability.</p>
          <div className="form-grid form-grid-two">
            <label>
              Public FRP host
              <input required value={frpHost} onChange={event => setFrpHost(event.target.value)} />
            </label>
            <label>
              Public FRP port
              <input required type="number" min={1} max={65535} value={frpPort} onChange={event => setFrpPort(event.target.value)} />
            </label>
          </div>
          <div className="node-option-row">
            <div>
              <strong>HTTP ingress</strong>
              <small>Optional public entry point for HTTP tunnels</small>
            </div>
            <Switch label="Enable HTTP ingress" checked={httpEnabled} onChange={setHTTPEnabled} />
          </div>
          {httpEnabled && (
            <div className="form-grid form-grid-two">
              <label>
                HTTP ingress host
                <input required value={httpHost} onChange={event => setHttpHost(event.target.value)} />
              </label>
              <label>
                HTTP ingress port
                <input required type="number" min={1} max={65535} value={httpPort} onChange={event => setHttpPort(event.target.value)} />
              </label>
            </div>
          )}
          <p className="form-hint">For Docker, publish the corresponding FRP and HTTP container ports. Public ports may differ when a proxy or port mapping is used.</p>
        </div>
      )}
      {error && <ErrorState message={error} />}
      <div className="modal-actions">
        <button type="button" disabled={saving} onClick={onClose}>Cancel</button>
        <button className="primary" type="submit" disabled={saving}>
          {saving ? <Spinner /> : <Save size={15} />}
          {mode === 'name' ? 'Save name' : mode === 'management' ? 'Save address' : 'Save endpoints'}
        </button>
      </div>
    </DialogShell>
  )
}
