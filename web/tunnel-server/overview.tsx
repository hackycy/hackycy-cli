import type { ClientView } from './api'
import type { NodeSummary } from './nodes-pages'
import { AlertCircle, ArrowRight, CheckCircle2, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { apiJson } from './api'
import { IconButton, navigate, Spinner, Status } from './ui'

export interface OverviewState {
  counts: { clients: number, connected: number, tunnels: number, pending: number, errors: number }
  server?: { frps: { state: string, pid?: number, error?: { message: string } } }
}

export interface AttentionItem {
  id: string
  label: string
  kind: 'Client' | 'Node' | 'Server'
  detail: string
  path: string
  severity: 'error' | 'warning'
}

export function attentionItems(clients: ClientView[], nodes: NodeSummary[], server?: OverviewState['server']): AttentionItem[] {
  const items: AttentionItem[] = []
  if (server && server.frps.state !== 'running') {
    items.push({
      id: 'server',
      label: 'Tunnel Server',
      kind: 'Server',
      path: '/server',
      severity: server.frps.state === 'recovering' ? 'warning' : 'error',
      detail: server.frps.error?.message ?? `FRPS ${server.frps.state.replaceAll('_', ' ')}`,
    })
  }
  for (const client of clients) {
    const label = client.remark || 'Unlabeled client'
    const path = `/clients/${encodeURIComponent(client.id)}`
    const error = client.runtime.lastError?.message
      ?? client.frpc.error?.message
      ?? client.restart.error?.message
      ?? (client.restart.state === 'failed' ? 'Client restart failed' : undefined)
      ?? (client.tunnelCounts.error ? `${client.tunnelCounts.error} tunnel ${client.tunnelCounts.error === 1 ? 'error' : 'errors'}` : undefined)
      ?? (client.runtime.connectionState === 'incompatible' ? 'Client version incompatible' : undefined)
    const warning = client.runtime.connectionState !== 'connected'
      ? `Client ${client.runtime.connectionState.replaceAll('_', ' ')}`
      : client.assignment.pendingNodeId
        ? 'Node switch waiting for client sync'
        : client.tunnelCounts.pending
          ? `${client.tunnelCounts.pending} pending tunnel ${client.tunnelCounts.pending === 1 ? 'change' : 'changes'}; revision ${client.lastAppliedRevision} / ${client.desiredRevision}`
          : client.lastAppliedRevision !== client.desiredRevision
            ? `Configuration revision ${client.lastAppliedRevision} / ${client.desiredRevision} awaiting sync`
            : client.restart.state === 'pending' ? 'Client restart pending' : undefined
    if (error || warning)
      items.push({ id: `client:${client.id}`, label, kind: 'Client', path, severity: error ? 'error' : 'warning', detail: error ?? warning ?? '' })
  }
  for (const node of nodes) {
    const path = `/nodes/${encodeURIComponent(node.id)}`
    const managementError = ['unreachable', 'identity_mismatch', 'incompatible', 'protocol_incompatible'].includes(node.management.state)
    const frpsError = ['configuration_failed', 'rollback_failed', 'apply_failed'].includes(node.frps.state) || (node.lifecycle === 'active' && node.frps.state === 'stopped' && node.selectability.reason === 'frps_stopped')
    const error = node.management.stale || node.frps.stale ? undefined : managementError ? `Management ${node.management.state.replaceAll('_', ' ')}` : frpsError ? `FRPS ${node.frps.state.replaceAll('_', ' ')}` : undefined
    const warning = node.management.stale || node.frps.stale
      ? 'Node status is historical'
      : node.lifecycle === 'removing'
        ? 'Removal pending'
        : !node.selectability.selectable
            ? node.selectability.reason?.replaceAll('_', ' ') ?? 'Node unavailable'
            : undefined
    if (error || warning)
      items.push({ id: `node:${node.id}`, label: node.name, kind: 'Node', path, severity: error ? 'error' : 'warning', detail: error ?? warning ?? '' })
  }
  const kindPriority = { Server: 0, Node: 1, Client: 2 }
  return items.sort((a, b) => Number(b.severity === 'error') - Number(a.severity === 'error') || kindPriority[a.kind] - kindPriority[b.kind] || a.label.localeCompare(b.label))
}

export function overviewIsHealthy(state: OverviewState, issues: AttentionItem[], loading: boolean, clientsError: string, nodesError: string): boolean {
  return !loading && !clientsError && !nodesError && issues.length === 0 && state.counts.connected === state.counts.clients && state.counts.errors === 0 && state.counts.pending === 0
}

export function Overview({ state, refreshSequence, refreshing, reload }: { state: OverviewState, refreshSequence: number, refreshing: boolean, reload: () => void }): React.JSX.Element {
  const [clients, setClients] = useState<ClientView[]>([])
  const [nodes, setNodes] = useState<NodeSummary[]>([])
  const [clientsError, setClientsError] = useState('')
  const [nodesError, setNodesError] = useState('')
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState(false)
  const requestId = useRef(0)
  const load = useCallback(async () => {
    const currentRequest = ++requestId.current
    setLoading(true)
    const [clientResult, nodeResult] = await Promise.allSettled([
      apiJson<{ clients: ClientView[] }>('/api/clients'),
      apiJson<{ nodes: NodeSummary[] }>('/api/nodes'),
    ])
    if (currentRequest !== requestId.current)
      return
    if (clientResult.status === 'fulfilled') {
      setClients(clientResult.value.clients)
      setClientsError('')
    }
    else {
      setClientsError(clientResult.reason instanceof Error ? clientResult.reason.message : String(clientResult.reason))
    }
    if (nodeResult.status === 'fulfilled') {
      setNodes(nodeResult.value.nodes)
      setNodesError('')
    }
    else {
      setNodesError(nodeResult.reason instanceof Error ? nodeResult.reason.message : String(nodeResult.reason))
    }
    setLoading(false)
  }, [])
  useEffect(() => void load(), [load, refreshSequence])
  const issues = attentionItems(clientsError ? [] : clients, nodesError ? [] : nodes, state.server)
  const visible = expanded ? issues : issues.slice(0, 6)
  const complete = !loading && !clientsError && !nodesError
  const healthy = overviewIsHealthy(state, issues, loading, clientsError, nodesError)
  const nodeStatusUnavailable = Boolean(nodesError || (loading && !nodes.length))
  const refresh = (): void => {
    reload()
    void load()
  }

  return (
    <>
      <header className="overview-heading">
        <div>
          <h1>Overview</h1>
          <p>Control plane health</p>
        </div>
        <IconButton label="Refresh overview" loading={refreshing || loading} onClick={refresh}><RefreshCw size={15} /></IconButton>
      </header>
      <section className="overview-signals" aria-label="Control plane health">
        {state.server && (
          <div className="overview-signal">
            <span>Server</span>
            <strong><Status value={state.server.frps.state} /></strong>
            <small>{state.server.frps.pid ? `FRPS PID ${state.server.frps.pid}` : 'No active process'}</small>
          </div>
        )}
        <div className="overview-signal">
          <span>Clients connected</span>
          <strong className="tabular">
            {state.counts.connected}
            <em>
              {' '}
              /
              {state.counts.clients}
            </em>
          </strong>
          <small>{state.counts.clients ? `${Math.round(state.counts.connected / state.counts.clients * 100)}% connected` : 'No trusted clients'}</small>
        </div>
        <div className="overview-signal">
          <span>Tunnel errors</span>
          <strong className={`tabular${state.counts.errors ? ' signal-error' : ''}`}>{state.counts.errors}</strong>
          <small>
            {state.counts.tunnels}
            {' '}
            definitions
          </small>
        </div>
        <div className="overview-signal">
          <span>Pending sync</span>
          <strong className={`tabular${state.counts.pending ? ' signal-warning' : ''}`}>{state.counts.pending}</strong>
          <small>Awaiting application</small>
        </div>
        <div className="overview-signal">
          <span>Nodes available</span>
          <strong className="tabular">
            {nodeStatusUnavailable ? '-' : nodes.filter(node => node.selectability.selectable).length}
            <em>
              {' '}
              /
              {nodeStatusUnavailable ? '-' : nodes.length}
            </em>
          </strong>
          <small>{nodesError ? 'Status unavailable' : 'Routing destinations'}</small>
        </div>
      </section>
      <section className="attention-section" aria-labelledby="attention-title" aria-busy={loading}>
        <div className="section-title attention-title">
          <h2 id="attention-title">Needs attention</h2>
          {complete && issues.length > 0 && <span className="attention-count tabular">{issues.length}</span>}
        </div>
        {clientsError && (
          <div className="attention-unavailable" role="alert">
            <AlertCircle size={16} />
            {' '}
            Client status unavailable:
            {' '}
            {clientsError}
            <button type="button" onClick={() => void load()}>Retry</button>
          </div>
        )}
        {nodesError && (
          <div className="attention-unavailable" role="alert">
            <AlertCircle size={16} />
            {' '}
            Node status unavailable:
            {' '}
            {nodesError}
            <button type="button" onClick={() => void load()}>Retry</button>
          </div>
        )}
        {loading && !clients.length && !nodes.length && (
          <div className="attention-loading" role="status">
            <Spinner />
            {' '}
            Loading object status
          </div>
        )}
        {!loading && state.counts.clients === 0 && !clientsError && (
          <div className="attention-empty">
            <div>
              <strong>No trusted clients yet</strong>
              <span>Create a client, then add its first tunnel route.</span>
            </div>
            <button type="button" onClick={() => navigate('/clients')}>
              Open Clients
              <ArrowRight size={15} />
            </button>
          </div>
        )}
        {visible.length > 0 && (
          <div className="attention-list">
            {visible.map(issue => (
              <button className="attention-row" type="button" key={issue.id} onClick={() => navigate(issue.path)}>
                <span className={`attention-marker attention-${issue.severity}`} aria-hidden="true" />
                <span className="attention-identity">
                  <strong>{issue.label}</strong>
                  <small>{issue.kind}</small>
                </span>
                <span className="attention-detail">{issue.detail}</span>
                <ArrowRight size={15} aria-hidden="true" />
              </button>
            ))}
          </div>
        )}
        {complete && issues.length > 6 && <button className="attention-expand" type="button" onClick={() => setExpanded(value => !value)}>{expanded ? 'Show fewer' : `Show all ${issues.length}`}</button>}
        {healthy && state.counts.clients > 0 && (
          <div className="attention-healthy">
            <CheckCircle2 size={17} />
            <div>
              <strong>All systems clear</strong>
              <span>No client, node, or tunnel issues reported.</span>
            </div>
          </div>
        )}
        {complete && issues.length === 0 && !healthy && state.counts.clients > 0 && (
          <div className="attention-unavailable" role="status">
            <AlertCircle size={16} />
            {' '}
            Connection or tunnel totals indicate issues. Open Clients to inspect current state.
            <button type="button" onClick={() => navigate('/clients')}>Open Clients</button>
          </div>
        )}
      </section>
    </>
  )
}
