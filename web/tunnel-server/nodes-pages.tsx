import { ArrowRight, Network, Plus, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { apiJson } from './api'
import { NodeClaimDialog } from './node-claim'
import { NodeConfigurationEditor, NodeMetadataEditor, NodeReapplyButton, NodeRemovalControls, NodeTokenRotationButton } from './node-config'
import { EmptyState, ErrorState, IconButton, LoadingState, navigate, PageHeader, Status } from './ui'

export interface NodeEndpoint {
  host: string
  port: number
}

export interface NodeSummary {
  id: string
  name: string
  kind: 'local' | 'remote'
  lifecycle: 'active' | 'removing'
  advertisedFrpAddress: NodeEndpoint | null
  httpIngressAddress: NodeEndpoint | null
  management: { state: string, observedAt?: string, stale: boolean }
  frps: { state: string, observedAt?: string, stale: boolean }
  lastKnownFrps?: { state: string, observedAt?: string, stale: boolean }
  selectability: { selectable: boolean, reason: string | null }
}

export interface NodeManagementView extends NodeSummary {
  managementAddress?: string
  pendingManagementAddress?: string
  candidateError?: string
  nodeFingerprint?: string
  controllerFingerprint?: string
  desired: {
    revision: number
    digest?: string
    mode: string
    settings?: {
      bindAddress: string
      bindPort: number
      vhostHTTPPort: number
      portRangeStart: number
      portRangeEnd: number
      custom404Page: string
    }
  }
  observed: {
    highestAcceptedRevision: number
    appliedRevision: number
    failedRevision: number
    configuration: string
    frps: string
    observedAt?: string
    stale: boolean
    error?: { code: string, phase: string, revision: number }
  }
  tokenRotation: { state: 'idle' | 'pending_node' | 'apply_failed' | 'publishing_clients', revision?: number }
}

function endpoint(value: NodeEndpoint | null): string {
  return value ? `${value.host}:${value.port}` : 'Not set'
}

function observedAt(value?: string): string {
  if (!value)
    return 'No observation'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? 'Time unavailable' : date.toLocaleString()
}

function loadError(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}

export function NodesPage({ refreshSequence, isAdmin }: { refreshSequence: number, isAdmin: boolean }): React.JSX.Element {
  const [nodes, setNodes] = useState<NodeSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const requestID = useRef(0)
  const load = useCallback(async () => {
    const current = ++requestID.current
    setLoading(true)
    try {
      const response = await apiJson<{ nodes: NodeSummary[] }>('/api/nodes')
      if (current !== requestID.current)
        return
      setNodes(response.nodes)
      setError('')
    }
    catch (cause) {
      if (current === requestID.current)
        setError(loadError(cause))
    }
    finally {
      if (current === requestID.current)
        setLoading(false)
    }
  }, [])
  useEffect(() => void load(), [load, refreshSequence])

  return (
    <>
      <PageHeader
        title="Nodes"
        actions={(
          <>
            <IconButton label="Refresh nodes" loading={loading} onClick={() => void load()}><RefreshCw size={15} /></IconButton>
            {isAdmin && (
              <button className="primary" type="button" onClick={() => setAdding(true)}>
                <Plus size={15} />
                Add Node
              </button>
            )}
          </>
        )}
      />
      {loading && nodes.length === 0
        ? <LoadingState label="Loading nodes" />
        : error && nodes.length === 0
          ? <ErrorState message={error} onRetry={() => void load()} />
          : (
              <>
                {error && <ErrorState message={error} onRetry={() => void load()} />}
                {nodes.length === 0
                  ? <EmptyState icon={Network} title="No nodes" description="No Node is available." />
                  : (
                      <section className="table-wrap data-panel" aria-busy={loading}>
                        <table className="data-table nodes-table">
                          <thead>
                            <tr>
                              <th>Node</th>
                              <th>FRP address</th>
                              <th>HTTP ingress</th>
                              <th>Management</th>
                              <th>FRPS</th>
                              <th>Availability</th>
                              <th aria-label="Details" />
                            </tr>
                          </thead>
                          <tbody>
                            {nodes.map(node => (
                              <tr key={node.id}>
                                <td data-label="Node">
                                  <button className="entity-link" type="button" onClick={() => navigate(`/nodes/${encodeURIComponent(node.id)}`)}>{node.name}</button>
                                  <span className="node-kind">{node.kind}</span>
                                </td>
                                <td className="mono" data-label="FRP address">{endpoint(node.advertisedFrpAddress)}</td>
                                <td className="mono" data-label="HTTP ingress">{endpoint(node.httpIngressAddress)}</td>
                                <td data-label="Management">
                                  <Status value={node.management.state} />
                                  <small className="node-observed">{observedAt(node.management.observedAt)}</small>
                                </td>
                                <td data-label="FRPS">
                                  <Status value={node.frps.state} />
                                  <small className="node-observed">{node.lastKnownFrps ? `Last known ${node.lastKnownFrps.state} at ${observedAt(node.lastKnownFrps.observedAt)}` : observedAt(node.frps.observedAt)}</small>
                                </td>
                                <td data-label="Availability"><Status value={node.selectability.selectable ? 'available' : node.selectability.reason ?? 'unavailable'} /></td>
                                <td><IconButton label={`Open ${node.name}`} onClick={() => navigate(`/nodes/${encodeURIComponent(node.id)}`)}><ArrowRight size={15} /></IconButton></td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </section>
                    )}
              </>
            )}
      {adding && (
        <NodeClaimDialog
          onClose={() => setAdding(false)}
          onCreated={(id) => {
            setAdding(false)
            navigate(`/nodes/${encodeURIComponent(id)}`)
          }}
        />
      )}
    </>
  )
}

export function NodeDetailPage({ id, refreshSequence, isAdmin }: { id: string, refreshSequence: number, isAdmin: boolean }): React.JSX.Element {
  const [node, setNode] = useState<NodeSummary>()
  const [management, setManagement] = useState<NodeManagementView>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const requestID = useRef(0)
  const load = useCallback(async () => {
    const current = ++requestID.current
    setLoading(true)
    try {
      const path = `/api/nodes/${encodeURIComponent(id)}`
      const summary = await apiJson<{ node: NodeSummary }>(path)
      const detail = isAdmin ? await apiJson<{ node: NodeManagementView }>(`${path}/management`) : undefined
      if (current !== requestID.current)
        return
      setNode(summary.node)
      setManagement(detail?.node)
      setError('')
    }
    catch (cause) {
      if (current === requestID.current)
        setError(loadError(cause))
    }
    finally {
      if (current === requestID.current)
        setLoading(false)
    }
  }, [id, isAdmin])
  useEffect(() => void load(), [load, refreshSequence])

  if (loading && !node)
    return <LoadingState label="Loading node" />
  if (!node)
    return <ErrorState message={error || 'Node unavailable'} onRetry={() => void load()} />
  return (
    <>
      <PageHeader
        title={node.name}
        eyebrow={node.kind === 'local' ? 'Local Node' : 'Remote Node'}
        actions={(
          <div className="node-detail-actions">
            <IconButton label="Refresh node" loading={loading} onClick={() => void load()}><RefreshCw size={15} /></IconButton>
            {isAdmin && management && <NodeMetadataEditor node={management} onSaved={() => void load()} />}
            {isAdmin && management && <NodeConfigurationEditor node={management} onSaved={() => void load()} />}
            {isAdmin && management && <NodeReapplyButton node={management} onSaved={() => void load()} />}
            {isAdmin && management && <NodeTokenRotationButton node={management} onSaved={() => void load()} />}
            {isAdmin && management && <NodeRemovalControls node={management} onSaved={() => void load()} onForgotten={() => navigate('/nodes')} />}
          </div>
        )}
      />
      {error && <ErrorState message={error} onRetry={() => void load()} />}
      {node.lifecycle === 'removing' && (
        <p className="form-hint" role="status">Removal pending. The Node remains listed until its saved disabled state and stopped FRPS are confirmed. While management is offline, its old FRPS may still run.</p>
      )}
      <section className="section-band node-detail-band">
        <div className="section-title">
          <h2>Connection</h2>
          <Status value={node.lifecycle} />
        </div>
        <dl className="detail-grid">
          <dt>FRP address</dt>
          <dd className="mono break">{endpoint(node.advertisedFrpAddress)}</dd>
          <dt>HTTP ingress</dt>
          <dd className="mono break">{endpoint(node.httpIngressAddress)}</dd>
          <dt>Availability</dt>
          <dd><Status value={node.selectability.selectable ? 'available' : node.selectability.reason ?? 'unavailable'} /></dd>
          <dt>Management</dt>
          <dd>
            <Status value={node.management.state} />
            <span className="node-observed">{observedAt(node.management.observedAt)}</span>
          </dd>
          <dt>FRPS now</dt>
          <dd>
            <Status value={node.frps.state} />
            <span className="node-observed">{observedAt(node.frps.observedAt)}</span>
          </dd>
          {node.lastKnownFrps && (
            <>
              <dt>Last known FRPS</dt>
              <dd>
                {node.lastKnownFrps.state}
                {' '}
                at
                {' '}
                {observedAt(node.lastKnownFrps.observedAt)}
              </dd>
            </>
          )}
        </dl>
      </section>
      {management && (
        <>
          <section className="section-band node-detail-band">
            <div className="section-title"><h2>Identity & management</h2></div>
            <dl className="detail-grid">
              {management.managementAddress && (
                <>
                  <dt>Management address</dt>
                  <dd className="mono break">{management.managementAddress}</dd>
                </>
              )}
              {management.pendingManagementAddress && (
                <>
                  <dt>Pending address</dt>
                  <dd className="mono break">
                    {management.pendingManagementAddress}
                    {' '}
                    {management.candidateError && <Status value={management.candidateError} />}
                  </dd>
                </>
              )}
              {management.nodeFingerprint && (
                <>
                  <dt>Node fingerprint</dt>
                  <dd className="mono break">{management.nodeFingerprint}</dd>
                </>
              )}
              {management.controllerFingerprint && (
                <>
                  <dt>Controller fingerprint</dt>
                  <dd className="mono break">{management.controllerFingerprint}</dd>
                </>
              )}
            </dl>
          </section>
          <section className="section-band node-detail-band">
            <div className="section-title">
              <h2>Configuration</h2>
              <Status value={management.observed.configuration} />
            </div>
            <dl className="detail-grid">
              <dt>Saved target</dt>
              <dd>
                {management.desired.mode}
                , revision
                {' '}
                {management.desired.revision}
              </dd>
              <dt>Accepted by Node</dt>
              <dd className="tabular">{management.observed.highestAcceptedRevision}</dd>
              <dt>Applied by Node</dt>
              <dd className="tabular">{management.observed.appliedRevision}</dd>
              {management.kind === 'remote' && (
                <>
                  <dt>FRP Token rotation</dt>
                  <dd>
                    <Status value={management.tokenRotation.state} />
                    {management.tokenRotation.revision && (
                      <span className="node-observed">
                        Revision
                        {' '}
                        {management.tokenRotation.revision}
                      </span>
                    )}
                  </dd>
                </>
              )}
              {management.observed.failedRevision > 0 && (
                <>
                  <dt>Failed revision</dt>
                  <dd className="tabular">{management.observed.failedRevision}</dd>
                </>
              )}
              {management.observed.error && (
                <>
                  <dt>Failure</dt>
                  <dd>
                    <Status value={management.observed.error.code} />
                    {' '}
                    at
                    {' '}
                    {management.observed.error.phase}
                  </dd>
                </>
              )}
              <dt>Observed</dt>
              <dd>
                {observedAt(management.observed.observedAt)}
                {management.observed.stale ? ' (historical)' : ''}
              </dd>
            </dl>
          </section>
        </>
      )}
    </>
  )
}
