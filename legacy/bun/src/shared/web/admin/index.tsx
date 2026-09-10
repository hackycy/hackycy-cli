import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { ChevronRight, Menu, Moon, Network, Sun, X } from 'lucide-react'
import { useEffect, useState } from 'react'

export type AdminTheme = 'light' | 'dark'

export interface AdminBrand {
  name: string
  icon?: LucideIcon
}

export interface AdminNavigationItem {
  id: string
  label: string
  icon: LucideIcon
  onSelect: () => void
}

export interface AdminAccount {
  name: string
  detail: string
  initials?: string
}

export interface AdminBreadcrumb {
  label: string
  onSelect?: () => void
}

export interface AdminSummaryItem {
  label: string
  value: ReactNode
  detail?: ReactNode
  tone?: 'default' | 'success' | 'warning' | 'danger'
}

function Brand({ brand }: { brand: AdminBrand }): React.JSX.Element {
  const Icon = brand.icon ?? Network
  return (
    <div className="admin-brand">
      <span className="admin-brand-mark"><Icon size={17} aria-hidden="true" /></span>
      <span>{brand.name}</span>
    </div>
  )
}

function ThemeButton({ theme, onThemeChange }: { theme: AdminTheme, onThemeChange: (theme: AdminTheme) => void }): React.JSX.Element {
  const nextTheme = theme === 'light' ? 'dark' : 'light'
  const Icon = theme === 'light' ? Moon : Sun
  return (
    <button className="admin-icon-button" type="button" aria-label={`Switch to ${nextTheme} theme`} title={`Switch to ${nextTheme} theme`} onClick={() => onThemeChange(nextTheme)}>
      <Icon size={16} aria-hidden="true" />
    </button>
  )
}

function SidebarContent({ brand, navigation, activeNavigationId, account, accountActions, onNavigate }: {
  brand: AdminBrand
  navigation: AdminNavigationItem[]
  activeNavigationId: string
  account: AdminAccount
  accountActions?: ReactNode
  onNavigate?: () => void
}): React.JSX.Element {
  const select = (item: AdminNavigationItem): void => {
    item.onSelect()
    onNavigate?.()
  }
  return (
    <>
      <Brand brand={brand} />
      <nav className="admin-navigation" aria-label="Primary navigation">
        {navigation.map((item) => {
          const Icon = item.icon
          const active = item.id === activeNavigationId
          return (
            <button className={active ? 'admin-navigation-item active' : 'admin-navigation-item'} type="button" key={item.id} aria-current={active ? 'page' : undefined} onClick={() => select(item)}>
              <Icon size={17} aria-hidden="true" />
              <span>{item.label}</span>
            </button>
          )
        })}
      </nav>
      <div className="admin-sidebar-account">
        <span className="admin-account-avatar">{account.initials ?? account.name.slice(0, 2).toUpperCase()}</span>
        <div className="admin-account-summary">
          <strong>{account.name}</strong>
          <small>{account.detail}</small>
        </div>
        {accountActions && <div className="admin-account-actions">{accountActions}</div>}
      </div>
    </>
  )
}

export function useAdminTheme(theme: AdminTheme): void {
  useEffect(() => {
    const root = document.documentElement
    const previousTheme = root.dataset.adminTheme
    root.dataset.adminTheme = theme
    return () => {
      if (previousTheme)
        root.dataset.adminTheme = previousTheme
      else
        delete root.dataset.adminTheme
    }
  }, [theme])
}

export function AdminLoginShell({ brand, title, description, theme, onThemeChange, children }: {
  brand: AdminBrand
  title: string
  description?: string
  theme: AdminTheme
  onThemeChange: (theme: AdminTheme) => void
  children: ReactNode
}): React.JSX.Element {
  return (
    <main className="admin-login-shell">
      <header className="admin-login-topbar">
        <Brand brand={brand} />
        <ThemeButton theme={theme} onThemeChange={onThemeChange} />
      </header>
      <section className="admin-login-panel" aria-labelledby="admin-login-title">
        <div className="admin-login-heading">
          <p>CONTROL PLANE</p>
          <h1 id="admin-login-title">{title}</h1>
          {description && <span>{description}</span>}
        </div>
        {children}
      </section>
    </main>
  )
}

export function AdminPageHeader({ eyebrow, title, description, actions }: { eyebrow?: string, title: string, description?: ReactNode, actions?: ReactNode }): React.JSX.Element {
  return (
    <header className="admin-page-header">
      <div>
        {eyebrow && <p className="admin-eyebrow">{eyebrow}</p>}
        <h1>{title}</h1>
        {description && <span className="admin-page-description">{description}</span>}
      </div>
      {actions && <div className="admin-page-actions">{actions}</div>}
    </header>
  )
}

export function AdminSummaryStrip({ items, action }: { items: AdminSummaryItem[], action?: ReactNode }): React.JSX.Element {
  return (
    <section className="admin-summary-strip" aria-label="Workspace summary">
      <div className="admin-summary-items">
        {items.map(item => (
          <div className="admin-summary-item" key={item.label}>
            <span>{item.label}</span>
            <strong>{item.value}</strong>
            {item.detail && <small className={item.tone && item.tone !== 'default' ? `admin-summary-${item.tone}` : undefined}>{item.detail}</small>}
          </div>
        ))}
      </div>
      {action && <div className="admin-summary-action">{action}</div>}
    </section>
  )
}

export function AdminPage({ children }: { children: ReactNode }): React.JSX.Element {
  return <div className="admin-page">{children}</div>
}

export function AdminShell({
  brand,
  navigation,
  activeNavigationId,
  account,
  accountActions,
  breadcrumbs,
  onBack,
  theme,
  onThemeChange,
  headerActions,
  children,
}: {
  brand: AdminBrand
  navigation: AdminNavigationItem[]
  activeNavigationId: string
  account: AdminAccount
  accountActions?: ReactNode
  breadcrumbs: AdminBreadcrumb[]
  onBack?: () => void
  theme: AdminTheme
  onThemeChange: (theme: AdminTheme) => void
  headerActions?: ReactNode
  children: ReactNode
}): React.JSX.Element {
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false)
  useEffect(() => {
    if (!mobileNavigationOpen)
      return
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key === 'Escape')
        setMobileNavigationOpen(false)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [mobileNavigationOpen])
  return (
    <div className="admin-shell">
      <aside className="admin-sidebar">
        <SidebarContent
          brand={brand}
          navigation={navigation}
          activeNavigationId={activeNavigationId}
          account={account}
          accountActions={accountActions}
        />
      </aside>
      {mobileNavigationOpen && (
        <div className="admin-mobile-navigation" role="dialog" aria-modal="true" aria-label="Navigation">
          <button className="admin-mobile-navigation-backdrop" type="button" aria-label="Close navigation" onClick={() => setMobileNavigationOpen(false)} />
          <aside className="admin-mobile-sidebar">
            <button className="admin-mobile-navigation-close admin-icon-button" type="button" aria-label="Close navigation" onClick={() => setMobileNavigationOpen(false)}><X size={16} aria-hidden="true" /></button>
            <SidebarContent
              brand={brand}
              navigation={navigation}
              activeNavigationId={activeNavigationId}
              account={account}
              accountActions={accountActions}
              onNavigate={() => setMobileNavigationOpen(false)}
            />
          </aside>
        </div>
      )}
      <main className="admin-main">
        <header className="admin-topbar">
          <div className="admin-breadcrumbs">
            <button className="admin-menu-button admin-icon-button" type="button" aria-label="Open navigation" onClick={() => setMobileNavigationOpen(true)}><Menu size={17} aria-hidden="true" /></button>
            {onBack && <button className="admin-back-button admin-icon-button" type="button" aria-label="Go back" title="Go back" onClick={onBack}><ChevronRight size={17} aria-hidden="true" /></button>}
            {breadcrumbs.map((breadcrumb, index) => (
              <span className="admin-breadcrumb" key={`${breadcrumb.label}-${index}`}>
                {index > 0 && <ChevronRight size={15} aria-hidden="true" />}
                {breadcrumb.onSelect
                  ? <button type="button" onClick={breadcrumb.onSelect}>{breadcrumb.label}</button>
                  : <strong>{breadcrumb.label}</strong>}
              </span>
            ))}
          </div>
          <div className="admin-topbar-actions">
            <ThemeButton theme={theme} onThemeChange={onThemeChange} />
            {headerActions}
          </div>
        </header>
        <div className="admin-content">
          <div className="admin-content-scroll">{children}</div>
        </div>
      </main>
    </div>
  )
}
