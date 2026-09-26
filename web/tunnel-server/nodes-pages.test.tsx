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
    expect(markup).not.toContain('Edit configuration')
    expect(markup).not.toContain('More Node actions')
    expect(markup).not.toContain('Node fingerprint')
  })

  it('shows management controls only where the Node permits them', () => {
    const remote = renderDetail(summary, management, true)
    expect(remote).toContain('Edit configuration')
    expect(remote).toContain('More Node actions')
    expect(remote).toContain('Node fingerprint')
    expect(remote).toContain('Historical:')

    const local = renderDetail({ ...summary, kind: 'local', id: 'local' }, { ...management, kind: 'local', id: 'local' }, true)
    expect(local).not.toContain('More Node actions')
    expect(local).not.toContain('Edit configuration')
    expect(local).not.toContain('Identity &amp; management')
    expect(local).not.toContain('Saved target')
  })
})
