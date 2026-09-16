import { Cable, Pencil } from 'lucide-react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { EmptyState, FeedbackProvider, PageHeader, RowActionMenu, Status } from './ui'

describe('tunnel server UI', () => {
  it('renders page header context through the shared header', () => {
    const markup = renderToStaticMarkup(
      <PageHeader eyebrow="Client" title="Edge gateway" description="Connection and synchronization state." />,
    )

    expect(markup).toContain('admin-eyebrow')
    expect(markup).toContain('Edge gateway')
    expect(markup).toContain('Connection and synchronization state.')
  })

  it('renders status with a decorative dot and readable text', () => {
    const markup = renderToStaticMarkup(<Status value="sync_disconnected" />)

    expect(markup).toContain('status-dot')
    expect(markup).toContain('status-label')
    expect(markup).toContain('aria-hidden="true"')
    expect(markup).toContain('sync disconnected')
  })

  it('renders a compact empty state with its primary action', () => {
    const markup = renderToStaticMarkup(
      <EmptyState
        icon={Cable}
        title="No tunnels"
        description="Create a route for this client."
        action={<button type="button">New tunnel</button>}
      />,
    )

    expect(markup).toContain('empty-state')
    expect(markup).toContain('No tunnels')
    expect(markup).toContain('Create a route for this client.')
    expect(markup).toContain('New tunnel')
  })

  it('exposes a labelled action-menu trigger', () => {
    const markup = renderToStaticMarkup(
      <FeedbackProvider>
        <RowActionMenu
          label="Actions for Edge gateway"
          actions={[{ label: 'Edit client', icon: Pencil, onSelect: () => {} }]}
        />
      </FeedbackProvider>,
    )

    expect(markup).toContain('aria-label="Actions for Edge gateway"')
    expect(markup).toContain('aria-haspopup="menu"')
    expect(markup).toContain('aria-expanded="false"')
  })
})
