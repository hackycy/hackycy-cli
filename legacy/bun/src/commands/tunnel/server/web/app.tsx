import type { ReactNode } from 'react'
import type { CurrentAccount } from './api'
import { zodResolver } from '@hookform/resolvers/zod'
import { CloudCog, Gauge, KeyRound, LogOut, Play, Power, RefreshCw, RotateCcw, Save, Server, Shield, Square, Users } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { AdminLoginShell, AdminPage, AdminShell, AdminSummaryStrip, useAdminTheme } from '../../../../shared/web/admin'
import { AccountsPage } from './account-pages'
import { ApiError, apiJson, jsonRequest } from './api'
import { ClientDetailPage, ClientsPage } from './client-pages'
import { FormError, FormField } from './form'
import { DialogShell } from './primitives'
import { ErrorState, IconButton, navigate, PageHeader, Spinner, Status, useFeedback } from './ui'

interface ServerProjection {
  frps: { state: string, pid?: number, error?: { message: string } }
  settings: {
    address: string
    controlPort: number
    frpPort: number
    httpPort: number
    portRange: { start: number, end: number }
    advertiseFrpAddress?: { host: string, port: number }
    dataDir: string
    adminUser: string
  }
}

interface StateView {
  account: CurrentAccount
  counts: { clients: number, connected: number, tunnels: number, pending: number, errors: number }
  server?: ServerProjection
}

interface Custom404PageView {
  content: string
}

type Page = { name: 'overview' | 'clients' | 'accounts' | 'server' } | { name: 'client', id: string }

const loginSchema = z.object({
  username: z.string().min(1, 'Username is required'),
  password: z.string().min(1, 'Password is required'),
})

const passwordSchema = z.object({
  currentPassword: z.string().min(1, 'Current password is required'),
  newPassword: z.string().min(5, 'New password must be at least 5 characters').max(256),
  confirmation: z.string(),
}).refine(values => values.newPassword === values.confirmation, { path: ['confirmation'], message: 'New passwords do not match' })

type LoginValues = z.infer<typeof loginSchema>
type PasswordValues = z.infer<typeof passwordSchema>

function currentPage(): Page {
  const client = /^\/clients\/([^/]+)$/.exec(location.pathname)?.[1]
  if (client)
    return { name: 'client', id: decodeURIComponent(client) }
  if (location.pathname === '/clients')
    return { name: 'clients' }
  if (location.pathname === '/accounts')
    return { name: 'accounts' }
  if (location.pathname === '/server')
    return { name: 'server' }
  return { name: 'overview' }
}

function Login({ theme, onThemeChange, onLogin }: { theme: 'light' | 'dark', onThemeChange: (theme: 'light' | 'dark') => void, onLogin: () => void }): React.JSX.Element {
  const { notify } = useFeedback()
  const form = useForm<LoginValues>({ resolver: zodResolver(loginSchema), defaultValues: { username: '', password: '' } })
  const submitting = form.formState.isSubmitting
  const submit = form.handleSubmit(async (values) => {
    form.clearErrors('root.server')
    try {
      await apiJson('/api/session', jsonRequest('POST', values))
      notify('Signed in')
      onLogin()
    }
    catch (cause) {
      form.setError('root.server', { message: cause instanceof Error ? cause.message : String(cause) })
    }
  })
  return (
    <AdminLoginShell brand={{ name: 'HACKYCY TUNNEL', icon: CloudCog }} title="Sign in" theme={theme} onThemeChange={onThemeChange}>
      <form className="tunnel-login-form" aria-busy={submitting} onSubmit={submit}>
        <FormField label="Username" error={form.formState.errors.username}>
          <input {...form.register('username')} autoComplete="username" autoFocus disabled={submitting} aria-invalid={Boolean(form.formState.errors.username)} />
        </FormField>
        <FormField label="Password" error={form.formState.errors.password}>
          <input {...form.register('password')} type="password" autoComplete="current-password" disabled={submitting} aria-invalid={Boolean(form.formState.errors.password)} />
        </FormField>
        <FormError error={form.formState.errors.root?.server} />
        <button className="primary" type="submit" disabled={submitting}>
          {submitting && <Spinner />}
          {submitting ? 'Signing in...' : 'Sign in'}
        </button>
      </form>
    </AdminLoginShell>
  )
}

function PasswordEditor({ onClose, onChanged }: { onClose: () => void, onChanged: () => void }): React.JSX.Element {
  const { notify } = useFeedback()
  const form = useForm<PasswordValues>({ resolver: zodResolver(passwordSchema), defaultValues: { currentPassword: '', newPassword: '', confirmation: '' } })
  const saving = form.formState.isSubmitting
  const submit = form.handleSubmit(async ({ currentPassword, newPassword }) => {
    form.clearErrors('root.server')
    try {
      await apiJson('/api/session/password', jsonRequest('PUT', { currentPassword, newPassword }))
      notify('Password changed')
      onChanged()
    }
    catch (cause) {
      form.setError('root.server', { message: cause instanceof Error ? cause.message : String(cause) })
    }
  })
  return (
    <DialogShell open title="Change password" busy={saving} onOpenChange={open => !open && onClose()} onSubmit={submit}>
      <FormField label="Current password" error={form.formState.errors.currentPassword}>
        <input {...form.register('currentPassword')} type="password" autoComplete="current-password" autoFocus aria-invalid={Boolean(form.formState.errors.currentPassword)} />
      </FormField>
      <FormField label="New password" error={form.formState.errors.newPassword}>
        <input {...form.register('newPassword')} type="password" minLength={5} maxLength={256} autoComplete="new-password" aria-invalid={Boolean(form.formState.errors.newPassword)} />
      </FormField>
      <FormField label="Confirm password" error={form.formState.errors.confirmation}>
        <input {...form.register('confirmation')} type="password" minLength={5} maxLength={256} autoComplete="new-password" aria-invalid={Boolean(form.formState.errors.confirmation)} />
      </FormField>
      <FormError error={form.formState.errors.root?.server} />
      <div className="modal-actions">
        <button type="button" onClick={onClose}>Cancel</button>
        <button className="primary" type="submit" disabled={saving}>
          {saving && <Spinner />}
          {saving ? 'Saving...' : 'Change'}
        </button>
      </div>
    </DialogShell>
  )
}

function Layout({ page, account, children, loggingOut, onLogout, onChangePassword, theme, onThemeChange }: { page: Page, account: CurrentAccount, children: ReactNode, loggingOut: boolean, onLogout: () => void, onChangePassword: () => void, theme: 'light' | 'dark', onThemeChange: (theme: 'light' | 'dark') => void }): React.JSX.Element {
  const navigation = [
    { id: 'overview', label: 'Overview', icon: Gauge, onSelect: () => navigate('/') },
    { id: 'clients', label: 'Clients', icon: Users, onSelect: () => navigate('/clients') },
    ...(account.role === 'admin' ? [{ id: 'accounts', label: 'Accounts', icon: Shield, onSelect: () => navigate('/accounts') }, { id: 'server', label: 'Server', icon: Server, onSelect: () => navigate('/server') }] : []),
  ]
  const pageLabel = page.name === 'overview' ? 'Overview' : page.name === 'client' ? 'Client details' : page.name === 'clients' ? 'Clients' : page.name === 'accounts' ? 'Accounts' : 'Server'
  const breadcrumbs = page.name === 'client'
    ? [{ label: 'Clients', onSelect: () => navigate('/clients') }, { label: 'Client details' }]
    : [{ label: 'Tunnel Control' }, { label: pageLabel }]
  return (
    <AdminShell
      brand={{ name: 'HACKYCY TUNNEL', icon: CloudCog }}
      navigation={navigation}
      activeNavigationId={page.name === 'client' ? 'clients' : page.name}
      account={{ name: account.username, detail: account.role }}
      accountActions={(
        <>
          {!account.managedByEnvironment && <IconButton label="Change password" onClick={onChangePassword}><KeyRound size={15} /></IconButton>}
          <IconButton label="Sign out" loading={loggingOut} onClick={onLogout}><LogOut size={15} /></IconButton>
        </>
      )}
      breadcrumbs={breadcrumbs}
      onBack={page.name === 'client' ? () => navigate('/clients') : undefined}
      theme={theme}
      onThemeChange={onThemeChange}
    >
      <AdminPage>{children}</AdminPage>
    </AdminShell>
  )
}

function Overview({ state, refreshing, reload }: { state: StateView, refreshing: boolean, reload: () => void }): React.JSX.Element {
  const metrics = [
    { label: 'Trusted clients', value: state.counts.clients },
    { label: 'Connected', value: state.counts.connected, detail: `${state.counts.clients ? Math.round((state.counts.connected / state.counts.clients) * 100) : 0}% of trusted clients`, tone: 'success' as const },
    { label: 'Tunnel definitions', value: state.counts.tunnels },
    { label: 'Pending', value: state.counts.pending, detail: state.counts.pending ? 'Awaiting client sync' : 'No pending changes', tone: state.counts.pending ? 'warning' as const : 'default' as const },
    { label: 'Errors', value: state.counts.errors, detail: state.counts.errors ? 'Needs intervention' : 'No reported errors', tone: state.counts.errors ? 'danger' as const : 'success' as const },
  ]
  return (
    <>
      <PageHeader title="Overview" actions={<IconButton label="Refresh overview" loading={refreshing} onClick={reload}><RefreshCw size={15} /></IconButton>} />
      <AdminSummaryStrip items={metrics} />
      {state.server && (
        <section className="section-band">
          <div className="section-title">
            <h2>Runtime</h2>
            <Status value={state.server.frps.state} />
          </div>
          <dl className="detail-grid">
            <dt>frps process</dt>
            <dd>{state.server.frps.pid ? `PID ${state.server.frps.pid}` : 'No active process'}</dd>
            <dt>Control listener</dt>
            <dd>
              {state.server.settings.address}
              :
              {state.server.settings.controlPort}
            </dd>
            <dt>FRP bind</dt>
            <dd>
              {state.server.settings.address}
              :
              {state.server.settings.frpPort}
            </dd>
            <dt>HTTP vhost</dt>
            <dd>
              {state.server.settings.address}
              :
              {state.server.settings.httpPort}
            </dd>
          </dl>
          {state.server.frps.error && <p className="runtime-error">{state.server.frps.error.message}</p>}
        </section>
      )}
    </>
  )
}

function Custom404PageEditor({ refreshSequence }: { refreshSequence: number }): React.JSX.Element {
  const [content, setContent] = useState('')
  const [savedContent, setSavedContent] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const { notify } = useFeedback()
  const dirty = content !== savedContent
  const load = useCallback(async () => {
    setLoading(true)
    try {
      const page = await apiJson<Custom404PageView>('/api/server/frps/config/custom-404-page')
      setContent(page.content)
      setSavedContent(page.content)
      setError('')
    }
    catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => {
    if (!dirty && !saving)
      void load()
  }, [dirty, load, refreshSequence, saving])
  const save = async (): Promise<void> => {
    setSaving(true)
    try {
      const page = await apiJson<Custom404PageView>('/api/server/frps/config/custom-404-page', jsonRequest('PUT', { content }))
      setContent(page.content)
      setSavedContent(page.content)
      setError('')
      notify('Custom 404 page saved')
    }
    catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setSaving(false)
    }
  }
  return (
    <section className="section-band">
      <div className="section-title">
        <h2>Custom 404 page</h2>
        {loading && <Spinner size={15} />}
      </div>
      {error && <p className="runtime-error" role="alert">{error}</p>}
      <textarea
        className="custom-404-editor"
        value={content}
        rows={16}
        spellCheck={false}
        autoCapitalize="off"
        aria-label="Custom 404 page HTML"
        disabled={loading || saving}
        onChange={event => setContent(event.target.value)}
      />
      <div className="custom-404-actions">
        <IconButton label="Restore FRP default 404 page" disabled={loading || saving || !content} onClick={() => setContent('')}><RotateCcw size={15} /></IconButton>
        <button className="primary" type="button" disabled={loading || saving || !dirty} onClick={() => void save()}>
          {saving ? <Spinner /> : <Save size={15} />}
          Save
        </button>
      </div>
    </section>
  )
}

function ServerView({ server, reload, refreshSequence }: { server: ServerProjection, reload: () => Promise<void>, refreshSequence: number }): React.JSX.Element {
  const [error, setError] = useState('')
  const [pending, setPending] = useState<'start' | 'stop' | 'restart'>()
  const { notify } = useFeedback()
  const action = async (value: 'start' | 'stop' | 'restart'): Promise<void> => {
    setPending(value)
    try {
      await apiJson(`/api/server/frp/${value}`, { method: 'POST' })
      setError('')
      notify(`frps ${value} completed`)
      await reload()
    }
    catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
      await reload().catch(() => {})
    }
    finally {
      setPending(undefined)
    }
  }
  return (
    <>
      <PageHeader
        title="Tunnel Server"
        actions={(
          <>
            <IconButton label="Start frps" loading={pending === 'start'} disabled={Boolean(pending)} onClick={() => void action('start')}><Play size={15} /></IconButton>
            <IconButton label="Stop frps" loading={pending === 'stop'} disabled={Boolean(pending)} onClick={() => void action('stop')}><Square size={14} /></IconButton>
            <IconButton label="Restart frps" loading={pending === 'restart'} disabled={Boolean(pending)} onClick={() => void action('restart')}><Power size={15} /></IconButton>
          </>
        )}
      />
      {error && <p className="runtime-error" role="alert">{error}</p>}
      <section className="section-band">
        <div className="section-title">
          <h2>frps</h2>
          <Status value={server.frps.state} />
        </div>
        <dl className="detail-grid">
          <dt>Process</dt>
          <dd>{server.frps.pid ? `PID ${server.frps.pid}` : 'Stopped'}</dd>
        </dl>
        {server.frps.error && <p className="runtime-error">{server.frps.error.message}</p>}
      </section>
      <section className="section-band">
        <div className="section-title"><h2>Deployment settings</h2></div>
        <dl className="detail-grid">
          <dt>Control listener</dt>
          <dd>
            {server.settings.address}
            :
            {server.settings.controlPort}
          </dd>
          <dt>FRP bind</dt>
          <dd>
            {server.settings.address}
            :
            {server.settings.frpPort}
          </dd>
          <dt>HTTP vhost</dt>
          <dd>
            {server.settings.address}
            :
            {server.settings.httpPort}
          </dd>
          <dt>Server Port Pool</dt>
          <dd>
            {server.settings.portRange.start}
            -
            {server.settings.portRange.end}
            {' '}
            TCP/UDP
          </dd>
          <dt>Advertised FRP</dt>
          <dd>{server.settings.advertiseFrpAddress ? `${server.settings.advertiseFrpAddress.host}:${server.settings.advertiseFrpAddress.port}` : 'Derived from agent request'}</dd>
          <dt>Data directory</dt>
          <dd className="mono break">{server.settings.dataDir}</dd>
          <dt>Deployment Administrator</dt>
          <dd>{server.settings.adminUser}</dd>
        </dl>
      </section>
      <Custom404PageEditor refreshSequence={refreshSequence} />
    </>
  )
}

export function App(): React.JSX.Element {
  const [authenticated, setAuthenticated] = useState<boolean>()
  const [page, setPage] = useState<Page>(currentPage)
  const [state, setState] = useState<StateView>()
  const [refreshSequence, setRefreshSequence] = useState(0)
  const [changingPassword, setChangingPassword] = useState(false)
  const [stateLoading, setStateLoading] = useState(true)
  const [stateError, setStateError] = useState('')
  const [eventsConnected, setEventsConnected] = useState<boolean>()
  const [loggingOut, setLoggingOut] = useState(false)
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    try {
      return localStorage.getItem('ycy-tunnel-admin-theme') === 'dark' ? 'dark' : 'light'
    }
    catch {
      return 'light'
    }
  })
  const { notify } = useFeedback()
  useAdminTheme(theme)
  const changeTheme = (nextTheme: 'light' | 'dark'): void => {
    setTheme(nextTheme)
    try {
      localStorage.setItem('ycy-tunnel-admin-theme', nextTheme)
    }
    catch {}
  }
  const sessionEnded = useCallback(() => {
    setAuthenticated(false)
    setState(undefined)
    setChangingPassword(false)
    setStateError('')
    setEventsConnected(undefined)
  }, [])
  const load = useCallback(async () => {
    setStateLoading(true)
    try {
      setState(await apiJson<StateView>('/api/state'))
      setAuthenticated(true)
      setStateError('')
    }
    catch (cause) {
      if (cause instanceof ApiError && cause.status === 401)
        sessionEnded()
      else
        setStateError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      setStateLoading(false)
    }
  }, [sessionEnded])
  useEffect(() => {
    const changed = (): void => setPage(currentPage())
    window.addEventListener('popstate', changed)
    window.addEventListener('tunnel-authentication-required', sessionEnded)
    return () => {
      window.removeEventListener('popstate', changed)
      window.removeEventListener('tunnel-authentication-required', sessionEnded)
    }
  }, [sessionEnded])
  useEffect(() => void load(), [load])
  useEffect(() => {
    if (state && state.account.role !== 'admin' && ['accounts', 'server'].includes(page.name))
      navigate('/')
  }, [page.name, state])
  useEffect(() => {
    if (!authenticated)
      return
    const events = new EventSource('/api/events')
    events.onopen = () => setEventsConnected(true)
    events.onerror = () => setEventsConnected(false)
    events.onmessage = (message) => {
      const event = JSON.parse(message.data) as { event: 'changed' | 'session_revoked' }
      if (event.event === 'session_revoked') {
        sessionEnded()
        return
      }
      setRefreshSequence(value => value + 1)
      void load()
    }
    return () => events.close()
  }, [authenticated, load, sessionEnded])
  useEffect(() => {
    if (!authenticated)
      return
    const refresh = (): void => {
      void apiJson('/api/session').catch(() => {})
    }
    const interval = window.setInterval(refresh, 24 * 60 * 60 * 1000)
    return () => window.clearInterval(interval)
  }, [authenticated])
  const logout = async (): Promise<void> => {
    setLoggingOut(true)
    try {
      await apiJson('/api/session', { method: 'DELETE' })
      sessionEnded()
    }
    catch (cause) {
      notify(cause instanceof Error ? cause.message : String(cause), 'error')
    }
    finally {
      setLoggingOut(false)
    }
  }
  if (authenticated === undefined) {
    if (stateError) {
      return (
        <div className="boot boot-error">
          <ErrorState message={stateError} retrying={stateLoading} onRetry={() => void load()} />
        </div>
      )
    }
    return (
      <div className="boot">
        <Spinner size={18} />
        Loading session
      </div>
    )
  }
  if (!authenticated)
    return <Login theme={theme} onThemeChange={changeTheme} onLogin={() => void load()} />
  if (!state) {
    return (
      <div className="boot">
        <Spinner size={18} />
        Loading workspace
      </div>
    )
  }
  return (
    <Layout page={page} account={state.account} loggingOut={loggingOut} onLogout={() => void logout()} onChangePassword={() => setChangingPassword(true)} theme={theme} onThemeChange={changeTheme}>
      {eventsConnected === false && (
        <div className="sync-banner" role="status">
          <Spinner />
          Live updates disconnected. Reconnecting...
        </div>
      )}
      {stateError && <ErrorState message={stateError} retrying={stateLoading} onRetry={() => void load()} />}
      {page.name === 'overview' && <Overview state={state} refreshing={stateLoading} reload={() => void load()} />}
      {page.name === 'clients' && <ClientsPage refreshSequence={refreshSequence} showOwner={state.account.role === 'admin'} />}
      {page.name === 'client' && <ClientDetailPage id={page.id} refreshSequence={refreshSequence} showOwner={state.account.role === 'admin'} />}
      {page.name === 'accounts' && state.account.role === 'admin' && <AccountsPage currentAccountId={state.account.id} refreshSequence={refreshSequence} onSessionEnded={sessionEnded} />}
      {page.name === 'server' && state.server && <ServerView server={state.server} reload={load} refreshSequence={refreshSequence} />}
      {changingPassword && <PasswordEditor onClose={() => setChangingPassword(false)} onChanged={sessionEnded} />}
    </Layout>
  )
}
