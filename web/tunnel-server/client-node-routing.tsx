import type { FormEvent } from 'react'
import type { ClientView, TunnelView } from './api'
import type { NodeEndpoint, NodeSummary } from './nodes-pages'
import { ArrowRight, Network, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { apiJson, jsonRequest } from './api'
import { NodeObservation, NodeStatus } from './nodes-pages'
import { DialogShell } from './primitives'
import { Spinner, Status, Token, useFeedback } from './ui'

function endpoint(value: NodeEndpoint | null | undefined): string {
  return value ? `${value.host}:${value.port}` : 'Not set'
}

function nodeName(client: ClientView, nodes: NodeSummary[], id: string | null): string {
  if (!id)
    return 'Not yet reported'
  return nodes.find(node => node.id === id)?.name
    ?? (client.assignment.node?.id === id ? client.assignment.node.name : undefined)
    ?? (client.assignment.pendingNode?.id === id ? client.assignment.pendingNode.name : undefined)
    ?? id
}

export function nodeApplicationState(client: ClientView): 'applied' | 'pending' | 'error' {
  if (client.assignment.appliedNodeId === client.assignment.nodeId && client.lastAppliedRevision === client.desiredRevision)
    return 'applied'
  if (client.runtime.lastError || client.frpc.error)
    return 'error'
  return 'pending'
}

export function canSelectNode(client: ClientView, node: NodeSummary): boolean {
  return node.selectability.selectable && node.id !== client.assignment.nodeId && node.id !== client.assignment.pendingNodeId
}

function observed(node: NodeSummary | undefined, part: 'management' | 'frps'): React.JSX.Element | null {
  const state = node?.[part]
  return state && (state.observedAt || state.stale) ? <NodeObservation at={state.observedAt} stale={state.stale} /> : null
}

export function ClientRoutingOverview({ client, tunnels, nodes, nodesError, showOwner, cancelling, onChangeNode, onCancelPending }: {
  client: ClientView
  tunnels: TunnelView[]
  nodes: NodeSummary[]
  nodesError: string
  showOwner: boolean
  cancelling: boolean
  onChangeNode: () => void
  onCancelPending: () => void
}): React.JSX.Element {
  const current = nodes.find(node => node.id === client.assignment.nodeId)
  const pending = client.assignment.pendingNodeId ? nodes.find(node => node.id === client.assignment.pendingNodeId) : undefined
  const application = nodeApplicationState(client)
  const hasHTTP = tunnels.some(tunnel => tunnel.protocol === 'http')
  const pendingSince = client.assignment.pendingSince ? new Date(client.assignment.pendingSince) : undefined
  const pendingTime = pendingSince && !Number.isNaN(pendingSince.getTime()) ? pendingSince.toLocaleString() : undefined

  return (
    <section className="client-routing" aria-label="Client Node routing">
      <div className="client-routing-top">
        <div className="client-node-main">
          <div className="client-node-heading">
            <div>
              <span className="client-routing-label">Current Node</span>
              <h2>{nodeName(client, nodes, client.assignment.nodeId)}</h2>
              <span className="client-node-kind">{current?.kind === 'local' ? 'Local Node' : current?.kind === 'remote' ? 'Remote Node' : 'Assigned Node'}</span>
            </div>
            <button type="button" disabled={cancelling} onClick={onChangeNode}>
              <Network size={15} />
              Change Node
            </button>
          </div>
          <div className="client-node-health">
            <div>
              <span>Availability</span>
              <NodeStatus value={current ? current.selectability.selectable ? 'available' : current.selectability.reason ?? 'unavailable' : 'unknown'} />
            </div>
            <div>
              <span>Management</span>
              <NodeStatus value={current?.management.state ?? 'unknown'} stale={current?.management.stale} />
              {observed(current, 'management')}
            </div>
            <div>
              <span>FRPS</span>
              <NodeStatus value={current?.frps.state ?? client.assignment.node?.frps.state ?? 'unknown'} stale={current?.frps.stale ?? client.assignment.node?.frps.stale} />
              {observed(current, 'frps')}
            </div>
          </div>
          <dl className="client-node-endpoints">
            <div>
              <dt>FRP address</dt>
              <dd>{endpoint(current?.advertisedFrpAddress ?? client.assignment.node?.advertisedFrpAddress)}</dd>
            </div>
            {hasHTTP && (
              <div>
                <dt>HTTP ingress</dt>
                <dd>{endpoint(current?.httpIngressAddress ?? client.assignment.node?.httpIngressAddress)}</dd>
              </div>
            )}
          </dl>
          {nodesError && <p className="client-node-notice" role="status">Node health is unavailable. Open Change Node to retry loading the Node list.</p>}
        </div>
        <div className="client-runtime-summary" aria-label="Client runtime">
          <div>
            <span>Connection</span>
            <Status value={client.runtime.connectionState} />
          </div>
          <div>
            <span>Node application</span>
            <Status value={application} />
          </div>
          <div>
            <span>Configuration</span>
            <strong className="mono">{`rev ${client.lastAppliedRevision} / ${client.desiredRevision}`}</strong>
          </div>
          <div>
            <span>Process</span>
            <Status value={client.runtime.processState} />
          </div>
          <div>
            <span>Restart</span>
            <Status value={client.restart.state} />
          </div>
          <div>
            <span>FRPC connection</span>
            <Status value={client.frpc.connection} />
          </div>
          <div>
            <span>Proxy observation</span>
            <strong>
              {client.frpc.proxies.length}
              {' '}
              observed
            </strong>
          </div>
        </div>
      </div>

      {(client.assignment.pendingNodeId || client.assignment.appliedNodeId !== client.assignment.nodeId) && (
        <div className="client-route-strip" aria-label="Node assignment progress">
          <div className="client-route-step is-current">
            <span>Current Node</span>
            <strong>{nodeName(client, nodes, client.assignment.nodeId)}</strong>
          </div>
          <ArrowRight size={16} aria-hidden="true" />
          <div className={`client-route-step${client.assignment.pendingNodeId ? ' is-pending' : ''}`}>
            <span>Pending Node</span>
            <strong>{client.assignment.pendingNodeId ? nodeName(client, nodes, client.assignment.pendingNodeId) : 'No pending switch'}</strong>
          </div>
          <ArrowRight size={16} aria-hidden="true" />
          <div className="client-route-step">
            <span>Client applied Node</span>
            <strong>{nodeName(client, nodes, client.assignment.appliedNodeId)}</strong>
            <Status value={application} />
          </div>
        </div>
      )}

      {client.assignment.pendingNodeId && (
        <div className="client-pending-switch" role="status">
          <div>
            <strong>Switch pending</strong>
            <span>
              {pendingTime ? `Waiting since ${pendingTime}. ` : ''}
              The target will be checked when the Client reconnects.
            </span>
            {pending && !pending.selectability.selectable && <NodeStatus value={pending.selectability.reason ?? 'unavailable'} />}
          </div>
          <button type="button" disabled={cancelling} onClick={onCancelPending}>
            {cancelling && <Spinner />}
            {cancelling ? 'Cancelling...' : 'Cancel pending switch'}
          </button>
        </div>
      )}

      <div className="client-routing-identity">
        <div>
          <span>Client token</span>
          <Token value={client.token} />
        </div>
        {showOwner && (
          <div>
            <span>Owner</span>
            <strong>{client.owner.username}</strong>
          </div>
        )}
      </div>
    </section>
  )
}

interface AssignmentSnapshot {
  client: ClientView
  tunnels: TunnelView[]
  nodes: NodeSummary[]
}

export function ChangeNodeDialog({ clientId, onClose, onSaved }: { clientId: string, onClose: () => void, onSaved: () => void }): React.JSX.Element {
  const [snapshot, setSnapshot] = useState<AssignmentSnapshot>()
  const [selectedId, setSelectedId] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [saveError, setSaveError] = useState('')
  const { notify } = useFeedback()
  const reload = useCallback(async () => {
    setLoading(true)
    setLoadError('')
    setSaveError('')
    try {
      const [detail, listing] = await Promise.all([
        apiJson<{ client: ClientView, tunnels: TunnelView[] }>(`/api/clients/${encodeURIComponent(clientId)}`),
        apiJson<{ nodes: NodeSummary[] }>('/api/nodes'),
      ])
      setSnapshot({ ...detail, nodes: listing.nodes })
      setSelectedId('')
    }
    catch (cause) {
      setSnapshot(undefined)
      setLoadError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setLoading(false)
    }
  }, [clientId])
  useEffect(() => void reload(), [reload])

  const selected = snapshot?.nodes.find(node => node.id === selectedId)
  const canConfirm = Boolean(snapshot && selected && canSelectNode(snapshot.client, selected))
  const online = snapshot?.client.runtime.connectionState === 'connected'
  const hasHTTP = snapshot?.tunnels.some(tunnel => tunnel.protocol === 'http') ?? false
  const currentNode = snapshot?.nodes.find(node => node.id === snapshot.client.assignment.nodeId)
  const submit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (!canConfirm || !selected)
      return
    setSaving(true)
    setSaveError('')
    try {
      const result = await apiJson<{ assignment: { nodeId: string, pendingNodeId: string | null } }>(`/api/clients/${encodeURIComponent(clientId)}/node-assignment`, jsonRequest('PUT', { nodeId: selected.id }))
      notify(result.assignment.pendingNodeId ? 'Pending Node switch saved' : 'Node switch submitted; awaiting Client application')
      onSaved()
    }
    catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setSaving(false)
    }
  }

  return (
    <DialogShell
      open
      title="Change Client Node"
      className="change-node-modal"
      busy={saving}
      onOpenChange={open => !open && onClose()}
      onOpenAutoFocus={(event) => {
        event.preventDefault()
        if (event.currentTarget instanceof HTMLElement)
          event.currentTarget.focus()
      }}
      onSubmit={event => void submit(event)}
    >
      {loading && (
        <p className="change-node-loading">
          <Spinner />
          Loading Client and Node status...
        </p>
      )}
      {!loading && loadError && (
        <div className="change-node-load-error">
          <p role="alert">{loadError}</p>
          <button type="button" onClick={() => void reload()}>
            <RefreshCw size={15} />
            Retry
          </button>
        </div>
      )}
      {!loading && snapshot && (
        <>
          <p className="change-node-intro">Select a destination. No routing changes are made until you confirm.</p>
          <div className="change-node-list" role="radiogroup" aria-label="Destination Node">
            {snapshot.nodes.map((node) => {
              const isCurrent = node.id === snapshot.client.assignment.nodeId
              const isPending = node.id === snapshot.client.assignment.pendingNodeId
              const disabled = !canSelectNode(snapshot.client, node)
              return (
                <label className="change-node-option" data-selected={selectedId === node.id} data-disabled={disabled} key={node.id}>
                  <input type="radio" name="destination-node" value={node.id} checked={selectedId === node.id} disabled={disabled || saving} onChange={() => setSelectedId(node.id)} />
                  <span className="change-node-option-content">
                    <span className="change-node-option-heading">
                      <strong>{node.name}</strong>
                      <small>{isCurrent ? 'Current' : isPending ? 'Pending target' : node.kind === 'local' ? 'Local Node' : 'Remote Node'}</small>
                    </span>
                    <span className="change-node-option-status">
                      <NodeStatus value={node.selectability.selectable ? 'available' : node.selectability.reason ?? 'unavailable'} />
                      <span>{`Management: ${node.management.state}${node.management.stale ? ' (stale)' : ''}`}</span>
                      <span>{`FRPS: ${node.frps.state}${node.frps.stale ? ' (stale)' : ''}`}</span>
                    </span>
                    <span className="change-node-option-address">{`FRP ${endpoint(node.advertisedFrpAddress)}${hasHTTP ? ` / HTTP ${endpoint(node.httpIngressAddress)}` : ''}`}</span>
                  </span>
                </label>
              )
            })}
          </div>
          {selected && (
            <div className="change-node-impact" aria-live="polite">
              <strong>
                {nodeName(snapshot.client, snapshot.nodes, snapshot.client.assignment.nodeId)}
                {' '}
                <ArrowRight size={15} aria-hidden="true" />
                {' '}
                {selected.name}
              </strong>
              <p>
                {snapshot.tunnels.length}
                {' '}
                tunnel
                {' '}
                {snapshot.tunnels.length === 1 ? 'definition' : 'definitions'}
                {' '}
                will follow this Client. The destination may reject a conflicting port or hostname.
              </p>
              {hasHTTP && (
                <dl>
                  <dt>HTTP DNS target</dt>
                  <dd>
                    {currentNode?.httpIngressAddress?.host ?? snapshot.client.assignment.node?.httpIngressAddress?.host ?? 'Not set'}
                    {' '}
                    <ArrowRight size={14} aria-hidden="true" />
                    {' '}
                    {selected.httpIngressAddress?.host ?? 'Not set'}
                  </dd>
                </dl>
              )}
              <p>{online ? 'The assignment will change now. Client application is confirmed separately.' : 'This will be saved as a pending switch. The target must be available when the Client reconnects.'}</p>
            </div>
          )}
          {!selected && <p className="change-node-hint">{online ? 'Choose an available Node to review the switch.' : 'The Client is offline. Choose an available Node to queue a switch.'}</p>}
          {saveError && <p className="form-error" role="alert">{saveError}</p>}
        </>
      )}
      <div className="modal-actions">
        <button type="button" disabled={saving} onClick={onClose}>Cancel</button>
        <button className="primary" type="submit" disabled={!canConfirm || loading || saving}>
          {saving && <Spinner />}
          {saving ? 'Saving...' : online ? 'Confirm switch' : 'Save pending switch'}
        </button>
      </div>
    </DialogShell>
  )
}
