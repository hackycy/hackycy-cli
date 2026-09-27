import type { ClientView } from './api'
import type { NodeSummary } from './nodes-pages'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { canSelectNode, ClientRoutingOverview, nodeApplicationState } from './client-node-routing'
import { FeedbackProvider } from './ui'

const client: ClientView = {
  id: 'client-1',
  remark: 'Office gateway',
  token: 'client-token',
  desiredRevision: 3,
  lastAppliedRevision: 3,
  revocationPending: false,
  createdAt: '2026-09-27T00:00:00Z',
  rotatedAt: null,
  owner: { id: 'owner-1', username: 'operator' },
  restart: { state: 'idle', desiredGeneration: 0, completedGeneration: 0 },
  runtime: { connectionState: 'connected', processState: 'running' },
  tunnelCounts: { total: 0, enabled: 0, applied: 0, pending: 0, error: 0 },
  assignment: {
    nodeId: 'local',
    pendingNodeId: null,
    desiredNodeId: 'local',
    appliedNodeId: 'local',
    node: {
      id: 'local',
      name: 'Local control Node',
      advertisedFrpAddress: { host: 'frp.example.test', port: 7000 },
      httpIngressAddress: { host: 'http.example.test', port: 8080 },
      frps: { state: 'running', stale: false },
    },
  },
  frpc: { revision: 3, nodeId: 'local', process: 'running', connection: 'connected', proxies: [] },
}

const node: NodeSummary = {
  id: 'remote',
  name: 'Remote edge',
  kind: 'remote',
  lifecycle: 'active',
  advertisedFrpAddress: { host: 'remote.example.test', port: 7000 },
  httpIngressAddress: { host: 'web.example.test', port: 80 },
  management: { state: 'reachable', observedAt: '2026-09-27T00:00:00Z', stale: false },
  frps: { state: 'running', observedAt: '2026-09-27T00:00:00Z', stale: false },
  selectability: { selectable: true, reason: null },
}

describe('client Node routing', () => {
  it('only reports application after Node and revision both match', () => {
    expect(nodeApplicationState(client)).toBe('applied')
    expect(nodeApplicationState({ ...client, lastAppliedRevision: 2 })).toBe('pending')
    expect(nodeApplicationState({ ...client, assignment: { ...client.assignment, appliedNodeId: 'remote' } })).toBe('pending')
    expect(nodeApplicationState({ ...client, lastAppliedRevision: 2, runtime: { ...client.runtime, lastError: { code: 'APPLY_FAILED', message: 'failed' } } })).toBe('error')
  })

  it('requires a selectable destination distinct from current and pending targets', () => {
    expect(canSelectNode(client, node)).toBe(true)
    expect(canSelectNode(client, { ...node, id: 'local' })).toBe(false)
    expect(canSelectNode({ ...client, assignment: { ...client.assignment, pendingNodeId: 'remote' } }, node)).toBe(false)
    expect(canSelectNode(client, { ...node, selectability: { selectable: false, reason: 'node_offline' } })).toBe(false)
  })

  it('retains the assigned Node when the Node list is unavailable', () => {
    const markup = renderToStaticMarkup(
      <FeedbackProvider>
        <ClientRoutingOverview
          client={client}
          tunnels={[]}
          nodes={[]}
          nodesError="Node list unavailable"
          showOwner
          cancelling={false}
          onChangeNode={() => {}}
          onCancelPending={() => {}}
        />
      </FeedbackProvider>,
    )

    expect(markup).toContain('Local control Node')
    expect(markup).toContain('frp.example.test:7000')
    expect(markup).toContain('Node health is unavailable')
    expect(markup).toContain('Change Node')
    expect(markup).toContain('Node application')
    expect(markup).not.toContain('Node assignment progress')
  })

  it('expands the routing progress only while a switch or Node application needs attention', () => {
    const pending = renderToStaticMarkup(
      <FeedbackProvider>
        <ClientRoutingOverview
          client={{ ...client, assignment: { ...client.assignment, pendingNodeId: 'remote', pendingSince: '2026-09-27T00:00:00Z' } }}
          tunnels={[]}
          nodes={[node]}
          nodesError=""
          showOwner={false}
          cancelling={false}
          onChangeNode={() => {}}
          onCancelPending={() => {}}
        />
      </FeedbackProvider>,
    )
    expect(pending).toContain('Node assignment progress')
    expect(pending).toContain('Remote edge')
    expect(pending).toContain('Switch pending')

    const mismatched = renderToStaticMarkup(
      <FeedbackProvider>
        <ClientRoutingOverview
          client={{ ...client, assignment: { ...client.assignment, appliedNodeId: 'remote' } }}
          tunnels={[]}
          nodes={[node]}
          nodesError=""
          showOwner={false}
          cancelling={false}
          onChangeNode={() => {}}
          onCancelPending={() => {}}
        />
      </FeedbackProvider>,
    )
    expect(mismatched).toContain('Node assignment progress')
    expect(mismatched).toContain('Client applied Node')
  })
})
