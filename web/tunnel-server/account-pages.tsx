import type { AccountView } from './api'
import type { AccountRole } from './domain'
import type { ConfirmAction } from './ui'
import { zodResolver } from '@hookform/resolvers/zod'
import { KeyRound, Pencil, Plus, RefreshCw, Trash2, Users } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'
import { apiJson, jsonRequest } from './api'
import { FormError, FormField } from './form'
import { DialogShell, SegmentedControl } from './primitives'
import { ConfirmationDialog, EmptyState, ErrorState, IconButton, LoadingState, PageHeader, RowActionMenu, Spinner, Status, useFeedback } from './ui'

function accountSchema(creating: boolean) {
  return z.object({
    username: z.string().min(1, 'Username is required').max(64),
    password: z.string().max(256),
    role: z.enum(['user', 'admin']),
  }).superRefine((values, context) => {
    if (creating && values.password.length < 5)
      context.addIssue({ code: 'custom', path: ['password'], message: 'Password must be at least 5 characters' })
  })
}

const passwordResetSchema = z.object({ password: z.string().min(5, 'Password must be at least 5 characters').max(256) })

type AccountFormValues = z.infer<ReturnType<typeof accountSchema>>
type PasswordResetValues = z.infer<typeof passwordResetSchema>

function AccountEditor({ account, onClose, onSaved }: { account?: AccountView, onClose: () => void, onSaved: () => void }): React.JSX.Element {
  const creating = !account
  const { notify } = useFeedback()
  const form = useForm<AccountFormValues>({
    resolver: zodResolver(accountSchema(creating)),
    defaultValues: { username: account?.username ?? '', password: '', role: account?.role ?? 'user' },
  })
  const saving = form.formState.isSubmitting
  const submit = form.handleSubmit(async (values) => {
    form.clearErrors('root.server')
    try {
      if (account)
        await apiJson(`/api/accounts/${encodeURIComponent(account.id)}`, jsonRequest('PATCH', { role: values.role }))
      else
        await apiJson('/api/accounts', jsonRequest('POST', { username: values.username, password: values.password, role: values.role }))
      notify(account ? 'Account role saved' : 'Account created')
      onSaved()
    }
    catch (cause) {
      form.setError('root.server', { message: cause instanceof Error ? cause.message : String(cause) })
    }
  })
  return (
    <DialogShell open title={account ? 'Change account role' : 'Create account'} busy={saving} onOpenChange={open => !open && onClose()} onSubmit={submit}>
      {!account && (
        <>
          <FormField label="Username" error={form.formState.errors.username}>
            <input {...form.register('username')} maxLength={64} autoComplete="off" autoFocus aria-invalid={Boolean(form.formState.errors.username)} />
          </FormField>
          <FormField label="Password" error={form.formState.errors.password}>
            <input {...form.register('password')} type="password" minLength={5} maxLength={256} autoComplete="new-password" aria-invalid={Boolean(form.formState.errors.password)} />
          </FormField>
        </>
      )}
      <FormField label="Role" error={form.formState.errors.role}>
        <Controller name="role" control={form.control} render={({ field }) => <SegmentedControl label="Role" className="roles" value={field.value} onChange={value => field.onChange(value as AccountRole)} options={[{ value: 'admin', label: 'Administrator' }, { value: 'user', label: 'User' }]} />} />
      </FormField>
      <FormError error={form.formState.errors.root?.server} />
      <div className="modal-actions">
        <button type="button" onClick={onClose}>Cancel</button>
        <button className="primary" type="submit" disabled={saving}>
          {saving && <Spinner />}
          {saving ? 'Saving...' : account ? 'Save' : 'Create'}
        </button>
      </div>
    </DialogShell>
  )
}

function PasswordReset({ account, onClose, onSaved }: { account: AccountView, onClose: () => void, onSaved: () => void }): React.JSX.Element {
  const form = useForm<PasswordResetValues>({ resolver: zodResolver(passwordResetSchema), defaultValues: { password: '' } })
  const saving = form.formState.isSubmitting
  const { notify } = useFeedback()
  const submit = form.handleSubmit(async ({ password }) => {
    form.clearErrors('root.server')
    try {
      await apiJson(`/api/accounts/${encodeURIComponent(account.id)}/password`, jsonRequest('PUT', { password }))
      notify('Account password reset')
      onSaved()
    }
    catch (cause) {
      form.setError('root.server', { message: cause instanceof Error ? cause.message : String(cause) })
    }
  })
  return (
    <DialogShell open title="Reset password" busy={saving} onOpenChange={open => !open && onClose()} onSubmit={submit}>
      <FormField label="New password" error={form.formState.errors.password}>
        <input {...form.register('password')} type="password" minLength={5} maxLength={256} autoComplete="new-password" autoFocus aria-invalid={Boolean(form.formState.errors.password)} />
      </FormField>
      <FormError error={form.formState.errors.root?.server} />
      <div className="modal-actions">
        <button type="button" onClick={onClose}>Cancel</button>
        <button className="primary" type="submit" disabled={saving}>
          {saving && <Spinner />}
          {saving ? 'Saving...' : 'Reset'}
        </button>
      </div>
    </DialogShell>
  )
}

export function AccountsPage({ currentAccountId, refreshSequence, onSessionEnded }: { currentAccountId: string, refreshSequence: number, onSessionEnded: () => void }): React.JSX.Element {
  const [accounts, setAccounts] = useState<AccountView[]>([])
  const [editing, setEditing] = useState<AccountView | null | undefined>()
  const [resetting, setResetting] = useState<AccountView>()
  const [confirmation, setConfirmation] = useState<ConfirmAction>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const loaded = useRef(false)
  const requestId = useRef(0)
  const load = useCallback(async () => {
    const currentRequest = ++requestId.current
    loaded.current ? setRefreshing(true) : setLoading(true)
    try {
      const result = (await apiJson<{ accounts: AccountView[] }>('/api/accounts')).accounts
      if (currentRequest !== requestId.current)
        return
      setAccounts(result)
      setError('')
      loaded.current = true
    }
    catch (cause) {
      if (currentRequest === requestId.current)
        setError(cause instanceof Error ? cause.message : String(cause))
    }
    finally {
      if (currentRequest === requestId.current) {
        setLoading(false)
        setRefreshing(false)
      }
    }
  }, [])
  useEffect(() => void load(), [load, refreshSequence])
  const remove = async (account: AccountView): Promise<void> => {
    await apiJson(`/api/accounts/${encodeURIComponent(account.id)}`, { method: 'DELETE' })
    if (account.id === currentAccountId)
      onSessionEnded()
    else
      await load()
  }
  return (
    <>
      <PageHeader
        title="Control Plane Accounts"
        description="Manage administrators and users allowed to own trusted tunnel clients."
        actions={(
          <>
            <IconButton label="Refresh accounts" loading={refreshing} onClick={() => void load()}><RefreshCw size={15} /></IconButton>
            <button className="primary" type="button" onClick={() => setEditing(null)}>
              <Plus size={15} />
              Create account
            </button>
          </>
        )}
      />
      {loading && !loaded.current
        ? <LoadingState label="Loading accounts" />
        : error && !loaded.current
          ? <ErrorState message={error} retrying={loading} onRetry={() => void load()} />
          : (
              <>
                {error && <ErrorState message={error} retrying={refreshing} onRetry={() => void load()} />}
                <section className="table-wrap data-panel" aria-busy={refreshing}>
                  <table className="data-table accounts-table">
                    <thead>
                      <tr>
                        <th>Account</th>
                        <th>Role</th>
                        <th>Clients</th>
                        <th aria-label="Actions" />
                      </tr>
                    </thead>
                    <tbody>
                      {accounts.map(account => (
                        <tr key={account.id}>
                          <td className="entity-cell" data-label="Account">
                            <div className="entity-content">
                              <strong>{account.username}</strong>
                              <span>{account.managedByEnvironment ? 'Environment managed' : 'Local account'}</span>
                            </div>
                          </td>
                          <td data-label="Role"><Status value={account.role} /></td>
                          <td className="tabular" data-label="Clients">{account.clientCount}</td>
                          <td data-label="Actions">
                            <RowActionMenu
                              label={`Actions for ${account.username}`}
                              actions={[
                                { label: 'Change role', icon: Pencil, disabled: account.managedByEnvironment, disabledReason: 'Managed by environment', onSelect: () => setEditing(account) },
                                { label: 'Reset password', icon: KeyRound, disabled: account.managedByEnvironment, disabledReason: 'Managed by environment', onSelect: () => setResetting(account) },
                                { label: 'Delete account', icon: Trash2, destructive: true, disabled: account.managedByEnvironment || account.clientCount > 0, disabledReason: account.managedByEnvironment ? 'Managed by environment' : account.clientCount ? 'Delete owned clients first' : undefined, onSelect: () => setConfirmation({ message: `Delete account ${account.username}?`, successMessage: 'Account deleted', action: () => remove(account) }) },
                              ]}
                            />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {!accounts.length && (
                    <EmptyState
                      icon={Users}
                      title="No local accounts"
                      description="Create an account to delegate access to the tunnel control plane."
                      action={(
                        <button className="primary" type="button" onClick={() => setEditing(null)}>
                          <Plus size={15} />
                          Create account
                        </button>
                      )}
                    />
                  )}
                </section>
              </>
            )}
      {editing !== undefined && (
        <AccountEditor
          account={editing ?? undefined}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined)
            if (editing?.id === currentAccountId)
              onSessionEnded()
            else
              void load()
          }}
        />
      )}
      {resetting && (
        <PasswordReset
          account={resetting}
          onClose={() => setResetting(undefined)}
          onSaved={() => {
            const self = resetting.id === currentAccountId
            setResetting(undefined)
            if (self)
              onSessionEnded()
            else
              void load()
          }}
        />
      )}
      {confirmation && <ConfirmationDialog request={confirmation} onClose={() => setConfirmation(undefined)} />}
    </>
  )
}
