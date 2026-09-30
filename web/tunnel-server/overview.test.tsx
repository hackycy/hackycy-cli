import type { ClientView } from './api'
import type { NodeSummary } from './nodes-pages'
import type { OverviewState } from './overview'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { attentionItems, Overview, overviewIsHealthy } from './overview'
import { FeedbackProvider } from './ui'

const healthyClient: ClientView = {
  id: 'client-1',
  remark: 'Office gateway',
  token: 'client-token',
  owner: { id: 'account-1', username: 'operator' },
  desiredRevision: 2,
  lastAppliedRevision: 2,
  revocationPending: false,
  createdAt: '2026-09-30T00:00:00Z',
  rotatedAt: null,
  restart: { state: 'idle', desiredGeneration: 0, completedGeneration: 0 },
  assignment: { nodeId: 'node-1', pendingNodeId: null, desiredNodeId: 'node-1', appliedNodeId: 'node-1' },
  frpc: { revision: 2, nodeId: 'node-1', process: 'running', connection: 'connected', proxies: [] },
  runtime: { connectionState: 'connected', processState: 'running' },
  tunnelCounts: { total: 2, enabled: 2, applied: 2, pending: 0, error: 0 },
}

const healthyNode: NodeSummary = {
  id: 'node-1',
  name: 'East edge',
  kind: 'remote',
  lifecycle: 'active',
  advertisedFrpAddress: { host: 'east.example.test', port: 7000 },
  httpIngressAddress: null,
  management: { state: 'reachable', stale: false },
  frps: { state: 'running', stale: false },
  selectability: { selectable: true, reason: null },
}

const state: OverviewState = { counts: { clients: 1, connected: 1, tunnels: 2, pending: 0, errors: 0 } }

describe('tunnel overview', () => {
  it('prioritizes current failures and links directly to affected objects', () => {
    const client: ClientView = { ...healthyClient, id: 'client/a', runtime: { ...healthyClient.runtime, lastError: { code: 'FAILED', message: 'Proxy rejected' } } }
    const node: NodeSummary = { ...healthyNode, management: { state: 'unreachable', stale: false }, selectability: { selectable: false, reason: 'node_offline' } }
    const issues = attentionItems([client], [node], { frps: { state: 'stopped' } })

    expect(issues.map(issue => issue.kind)).toEqual(['Server', 'Node', 'Client'])
    expect(issues.map(issue => issue.severity)).toEqual(['error', 'error', 'error'])
    expect(issues[2]).toMatchObject({ path: '/clients/client%2Fa', detail: 'Proxy rejected' })
    expect(attentionItems([healthyClient], [healthyNode])).toEqual([])
  })

  it('treats historical node status and pending client revisions as warnings', () => {
    const node: NodeSummary = { ...healthyNode, management: { state: 'unreachable', stale: true }, selectability: { selectable: false, reason: 'node_offline' } }
    const client: ClientView = { ...healthyClient, desiredRevision: 3 }
    const issues = attentionItems([client], [node])

    expect(issues[0]).toMatchObject({ kind: 'Node', severity: 'warning', detail: 'Node status is historical' })
    expect(issues[1]).toMatchObject({ kind: 'Client', severity: 'warning', detail: 'Configuration revision 2 / 3 awaiting sync' })
  })

  it('never reports all clear while object status is unavailable or loading', () => {
    expect(overviewIsHealthy(state, [], false, '', '')).toBe(true)
    expect(overviewIsHealthy(state, [], true, '', '')).toBe(false)
    expect(overviewIsHealthy(state, [], false, 'Network error', '')).toBe(false)
    expect(overviewIsHealthy(state, [], false, '', 'Network error')).toBe(false)
    expect(overviewIsHealthy({ ...state, counts: { ...state.counts, errors: 1 } }, [], false, '', '')).toBe(false)
    expect(overviewIsHealthy({ ...state, counts: { ...state.counts, connected: 0 } }, [], false, '', '')).toBe(false)
  })

  it('shows server health only when the state includes administrator data', () => {
    const render = (overviewState: OverviewState): string => renderToStaticMarkup(
      <FeedbackProvider>
        <Overview state={overviewState} refreshSequence={0} refreshing={false} reload={() => {}} />
      </FeedbackProvider>,
    )

    expect(render(state)).not.toContain('<span>Server</span>')
    expect(render({ ...state, server: { frps: { state: 'running', pid: 42 } } })).toContain('FRPS PID 42')
  })
})
