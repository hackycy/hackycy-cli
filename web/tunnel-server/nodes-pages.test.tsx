import type { NodeManagementView, NodeSummary } from './nodes-pages'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { NodeDetailContent, NodeObservation, NodeStatus } from './nodes-pages'
import { FeedbackProvider } from './ui'

const summary: NodeSummary = {
  id: 'edge-east',
  name: 'Edge east',
  kind: 'remote',
  lifecycle: 'active',
  advertisedFrpAddress: { host: 'edge.example.test', port: 7000 },
  httpIngressAddress: { host: 'http.example.test', port: 8080 },
  management: { state: 'unreachable', observedAt: '2026-09-27T00:00:00Z', stale: true },
  frps: { state: 'unknown', observedAt: '2026-09-27T00:00:00Z', stale: true },
  lastKnownFrps: { state: 'running', observedAt: '2026-09-26T00:00:00Z', stale: true },
  selectability: { selectable: false, reason: 'node_offline' },
}

const management: NodeManagementView = {
  ...summary,
  managementAddress: 'http://edge.example.test:7600',
  nodeFingerprint: 'sha256:node-fingerprint',
  desired: { revision: 3, mode: 'running' },
  observed: {
    highestAcceptedRevision: 3,
    appliedRevision: 2,
    failedRevision: 0,
    configuration: 'unknown',
    frps: 'unknown',
    observedAt: '2026-09-27T00:00:00Z',
    stale: true,
  },
  tokenRotation: { state: 'idle' },
}

const configured: NodeManagementView = {
  ...management,
  management: { state: 'reachable', observedAt: '2026-09-27T00:00:00Z', stale: false },
  frps: { state: 'running', observedAt: '2026-09-27T00:00:00Z', stale: false },
  selectability: { selectable: true, reason: null },
  desired: {
    revision: 2,
    mode: 'running',
    settings: { bindAddress: '0.0.0.0', bindPort: 7001, vhostHTTPPort: 8081, portRangeStart: 20000, portRangeEnd: 20100, custom404Page: '' },
  },
  observed: { highestAcceptedRevision: 2, appliedRevision: 2, failedRevision: 0, configuration: 'converged', frps: 'running', stale: false },
}

function renderDetail(node: NodeSummary, detail: NodeManagementView | undefined, isAdmin: boolean): string {
  return renderToStaticMarkup(
    <FeedbackProvider>
      <NodeDetailContent node={node} management={detail} isAdmin={isAdmin} onReload={() => {}} />
    </FeedbackProvider>,
  )
}

describe('node presentation', () => {
  it('distinguishes current failures from historical observations', () => {
    expect(renderToStaticMarkup(<NodeStatus value="node_offline" />)).toContain('status-error')
    expect(renderToStaticMarkup(<NodeStatus value="running" stale />)).toContain('status-muted')
    expect(renderToStaticMarkup(<NodeObservation at="2026-09-27T00:00:00Z" stale />)).toContain('Historical:')
  })

  it('keeps current and last-known FRPS separate for a read-only viewer', () => {
    const markup = renderDetail(summary, undefined, false)
    expect(markup).toContain('FRPS now')
    expect(markup).toContain('Last known running')
    expect(markup).toContain('Historical:')
    expect(markup).not.toContain('Node setup')
    expect(markup).not.toContain('More Node actions')
    expect(markup).not.toContain('Node fingerprint')
  })

  it('shows management controls only where the Node permits them', () => {
    const remote = renderDetail(summary, management, true)
    expect(remote).toContain('Node setup')
    expect(remote).toContain('Configure FRPS')
    expect(remote).toContain('More Node actions')
    expect(remote).toContain('Node fingerprint')
    expect(remote).toContain('Historical:')

    const local = renderDetail({ ...summary, kind: 'local', id: 'local' }, { ...management, kind: 'local', id: 'local' }, true)
    expect(local).not.toContain('More Node actions')
    expect(local).not.toContain('Node setup')
    expect(local).not.toContain('Identity &amp; management')
    expect(local).not.toContain('Saved target')
  })

  it('shows required setup and keeps HTTP ingress optional', () => {
    const unconfigured: NodeManagementView = {
      ...configured,
      advertisedFrpAddress: null,
      httpIngressAddress: null,
      desired: { revision: 0, mode: 'unconfigured' },
      observed: { highestAcceptedRevision: 0, appliedRevision: 0, failedRevision: 0, configuration: 'unconfigured', frps: 'stopped', stale: false },
      selectability: { selectable: false, reason: 'unconfigured' },
    }
    const initial = renderDetail(unconfigured, unconfigured, true)
    expect(initial).toContain('Configure FRPS')
    expect(initial).toContain('Set endpoints')
    expect(initial).toContain('Waiting for setup')
    expect(initial).toContain('aria-valuenow="1"')
    expect(initial).toContain('node-setup-pending node-setup-current')

    const withoutHTTP = renderDetail({ ...configured, httpIngressAddress: null }, { ...configured, httpIngressAddress: null }, true)
    expect(withoutHTTP).toContain('Optional, not set')
    expect(withoutHTTP).toContain('Ready to assign')
    expect(withoutHTTP).toContain('Revision 2 is applied and FRPS is running')
    expect(withoutHTTP).toContain('aria-valuenow="4"')
    expect(withoutHTTP.match(/node-setup-saved/g)).toHaveLength(2)
    expect(withoutHTTP).toContain('node-setup-segment-complete')
  })

  it('separates saved, applied, failed, and historical revisions', () => {
    const pending: NodeManagementView = {
      ...configured,
      frps: { state: 'stopped', stale: false },
      selectability: { selectable: false, reason: 'frps_stopped' },
      observed: { highestAcceptedRevision: 0, appliedRevision: 0, failedRevision: 0, configuration: 'pending', frps: 'stopped', stale: false },
    }
    expect(renderDetail(pending, pending, true)).toContain('Saved revision 2; accepted by Node 0.')
    expect(renderDetail(pending, pending, true)).toContain('Waiting for Node')
    expect(renderDetail(pending, pending, true)).toContain('aria-valuenow="3"')

    const failed: NodeManagementView = {
      ...configured,
      observed: {
        highestAcceptedRevision: 2,
        appliedRevision: 1,
        failedRevision: 2,
        configuration: 'apply_failed_old_running',
        frps: 'running',
        stale: false,
        error: { code: 'NODE_APPLY_FAILED', phase: 'failed', revision: 2 },
      },
    }
    const failedMarkup = renderDetail(failed, failed, true)
    expect(failedMarkup).toContain('Apply failed')
    expect(failedMarkup).toContain('Previous revision 1 is still running')
    expect(failedMarkup).toContain('NODE_APPLY_FAILED')
    expect(failedMarkup).toContain('node-setup-error node-setup-current')
    expect(failedMarkup).not.toContain('Revision 2 is applied and FRPS is running')

    const stale: NodeManagementView = {
      ...configured,
      management: { state: 'unknown', stale: true },
      frps: { state: 'unknown', stale: true },
      observed: { ...configured.observed, stale: true },
      selectability: { selectable: false, reason: 'node_offline' },
    }
    const staleMarkup = renderDetail(stale, stale, true)
    expect(staleMarkup).toContain('No current observation')
    expect(staleMarkup).toContain('aria-valuenow="2"')
    expect(staleMarkup).not.toContain('node-setup-error node-setup-current')
    expect(staleMarkup).not.toContain('Revision 2 is applied and FRPS is running')
  })
})
