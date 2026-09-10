import { describe, expect, test } from 'bun:test'
import { Gauge, Users } from 'lucide-react'
import { renderToStaticMarkup } from 'react-dom/server'
import { AdminLoginShell, AdminPage, AdminShell, AdminSummaryStrip } from './index'

describe('Admin Panel', () => {
  test('renders the configured workspace navigation and summary', () => {
    const markup = renderToStaticMarkup(
      <AdminShell
        brand={{ name: 'Test Console' }}
        navigation={[
          { id: 'overview', label: 'Overview', icon: Gauge, onSelect: () => {} },
          { id: 'clients', label: 'Clients', icon: Users, onSelect: () => {} },
        ]}
        activeNavigationId="clients"
        account={{ name: 'operator@example.com', detail: 'admin' }}
        breadcrumbs={[{ label: 'Operations' }, { label: 'Clients' }]}
        theme="light"
        onThemeChange={() => {}}
      >
        <AdminPage><AdminSummaryStrip items={[{ label: 'Connected', value: 4, detail: 'All healthy', tone: 'success' }]} /></AdminPage>
      </AdminShell>,
    )

    expect(markup).toContain('Test Console')
    expect(markup).toContain('Clients')
    expect(markup).toContain('admin-navigation-item active')
    expect(markup).toContain('All healthy')
  })

  test('keeps authentication forms inside a labelled login region', () => {
    const markup = renderToStaticMarkup(
      <AdminLoginShell brand={{ name: 'Test Console' }} title="Sign in" theme="dark" onThemeChange={() => {}}>
        <form><input aria-label="Username" /></form>
      </AdminLoginShell>,
    )

    expect(markup).toContain('aria-labelledby="admin-login-title"')
    expect(markup).toContain('Switch to light theme')
    expect(markup).toContain('Username')
  })
})
