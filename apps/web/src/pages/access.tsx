import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, KeyRound, Plus, Shield, UserRound, UsersRound } from 'lucide-react'
import { useState, type FormEvent, type ReactNode } from 'react'

import {
  APIError,
  createManagedUser,
  getAccessOverview,
  getCurrentUser,
  resetManagedUserPassword,
  updateManagedUser,
  type AccessOverview,
  type ManagedUser,
} from '../api/client'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'

const newUserID = 'new'

export function AccessPage() {
  const queryClient = useQueryClient()
  const sessionQuery = useQuery({ queryKey: ['session'], queryFn: getCurrentUser })
  const accessQuery = useQuery({
    queryKey: ['access-overview'],
    queryFn: getAccessOverview,
    enabled: sessionQuery.data?.platformAdmin === true,
  })
  const [selectedUserID, setSelectedUserID] = useState<string>(newUserID)
  const [selectedApplicationID, setSelectedApplicationID] = useState('')

  const selectedUser = accessQuery.data?.users.find((user) => user.id === selectedUserID)
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['access-overview'] })
  }

  if (sessionQuery.isPending) {
    return <AccessSkeleton />
  }
  if (!sessionQuery.data?.platformAdmin) {
    return (
      <Card className="page-enter p-8">
        <Shield className="size-8 text-muted" />
        <h2 className="mt-5 text-2xl font-semibold tracking-[-0.03em]">Platform administrator only</h2>
        <p className="mt-2 max-w-xl text-sm leading-6 text-muted">Application roles control gateway access. User provisioning and platform administration stay restricted.</p>
      </Card>
    )
  }
  if (accessQuery.isPending) {
    return <AccessSkeleton />
  }
  if (accessQuery.isError) {
    return <ErrorState error={accessQuery.error} retry={() => accessQuery.refetch()} />
  }

  const overview = accessQuery.data
  const activeApplicationID = selectedApplicationID || overview.applications[0]?.id || ''
  const activeUsers = overview.users.filter((user) => user.enabled).length
  const administrators = overview.users.filter((user) => user.platformAdmin && user.enabled).length

  return (
    <div className="page-enter space-y-6">
      <header className="flex flex-col justify-between gap-4 border-b pb-6 md:flex-row md:items-end">
        <div>
          <p className="font-mono text-[10px] font-bold tracking-[0.18em] text-muted uppercase">Identity and access</p>
          <h2 className="mt-2 text-3xl font-semibold tracking-[-0.04em]">Users, roles, accountability.</h2>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-muted">Platform admins provision accounts. Application roles limit what each person can view or change.</p>
        </div>
        <Button onClick={() => setSelectedUserID(newUserID)} variant="primary">
          <Plus className="size-4" /> Add user
        </Button>
      </header>

      <section className="grid gap-px overflow-hidden rounded-xl border bg-border sm:grid-cols-3">
        <Metric icon={UsersRound} label="Total users" value={overview.users.length} />
        <Metric icon={Check} label="Enabled" value={activeUsers} />
        <Metric icon={Shield} label="Platform admins" value={administrators} />
      </section>

      <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
        <Card className="overflow-hidden">
          <div className="flex flex-col justify-between gap-3 border-b p-5 sm:flex-row sm:items-center">
            <div>
              <h3 className="font-semibold">Directory</h3>
              <p className="mt-1 text-xs text-muted">Select an account to change access.</p>
            </div>
            <label className="text-xs font-bold text-muted">
              Application
              <select className="field mt-1 min-w-52 text-sm font-medium text-foreground" onChange={(event) => setSelectedApplicationID(event.target.value)} value={activeApplicationID}>
                {overview.applications.map((application) => <option key={application.id} value={application.id}>{application.name}</option>)}
              </select>
            </label>
          </div>
          <div className="divide-y">
            {overview.users.map((user) => {
              const membership = user.memberships.find((item) => item.applicationId === activeApplicationID)
              const selected = user.id === selectedUserID
              return (
                <button
                  className={`grid w-full gap-3 px-5 py-4 text-left transition-colors hover:bg-surface-strong sm:grid-cols-[minmax(0,1fr)_170px_90px] sm:items-center ${selected ? 'bg-surface-strong' : ''}`}
                  key={user.id}
                  onClick={() => setSelectedUserID(user.id)}
                  type="button"
                >
                  <span className="min-w-0">
                    <span className="flex items-center gap-2">
                      <span className="truncate font-semibold">{user.displayName}</span>
                      {user.platformAdmin && <Shield className="size-3.5 text-lime-700 dark:text-lime-300" aria-label="Platform administrator" />}
                    </span>
                    <span className="mt-1 block truncate font-mono text-[11px] text-muted">{user.email}</span>
                  </span>
                  <span className="text-xs font-semibold text-muted">{membership?.roleName ?? 'No access'}</span>
                  <Badge tone={user.enabled ? 'success' : 'danger'}>{user.enabled ? 'Enabled' : 'Disabled'}</Badge>
                </button>
              )
            })}
          </div>
        </Card>

        <UserEditor
          applicationID={activeApplicationID}
          key={`${selectedUser?.id ?? newUserID}-${selectedUser?.version ?? 0}-${activeApplicationID}`}
          onSaved={async (userID) => {
            await refresh()
            setSelectedUserID(userID)
          }}
          overview={overview}
          sessionUserID={sessionQuery.data.id}
          user={selectedUser}
        />
      </div>

      <RolePolicy roles={overview.roles} />
      <AuditTrail logs={overview.auditLogs} />
    </div>
  )
}

function UserEditor({ applicationID, onSaved, overview, sessionUserID, user }: {
  applicationID: string
  onSaved: (userID: string) => Promise<void>
  overview: AccessOverview
  sessionUserID: string
  user?: ManagedUser
}) {
  const currentMembership = user?.memberships.find((membership) => membership.applicationId === applicationID)
  const [displayName, setDisplayName] = useState(user?.displayName ?? '')
  const [email, setEmail] = useState(user?.email ?? '')
  const [password, setPassword] = useState('')
  const [roleID, setRoleID] = useState(currentMembership?.roleId ?? '')
  const [platformAdmin, setPlatformAdmin] = useState(user?.platformAdmin ?? false)
  const [enabled, setEnabled] = useState(user?.enabled ?? true)
  const isCurrentUser = user?.id === sessionUserID

  const saveMutation = useMutation({
    mutationFn: async () => {
      const memberships = user ? [...user.memberships] : []
      const existingIndex = memberships.findIndex((membership) => membership.applicationId === applicationID)
      if (existingIndex >= 0) {
        memberships.splice(existingIndex, 1)
      }
      if (applicationID && roleID) {
        memberships.push({ applicationId: applicationID, applicationName: '', roleId: roleID, roleName: '' })
      }
      const input = {
        displayName,
        email,
        platformAdmin,
        memberships: memberships.map(({ applicationId, roleId }) => ({ applicationId, roleId })),
      }
      if (!user) {
        return createManagedUser({ ...input, password })
      }
      return updateManagedUser(user.id, { ...input, enabled, version: user.version })
    },
    onSuccess: async (savedUser) => onSaved(savedUser.id),
  })
  const passwordMutation = useMutation({
    mutationFn: () => resetManagedUserPassword(user?.id ?? '', password),
    onSuccess: async () => {
      setPassword('')
      if (user) await onSaved(user.id)
    },
  })

  const submit = (event: FormEvent) => {
    event.preventDefault()
    saveMutation.mutate()
  }

  return (
    <Card className="xl:sticky xl:top-20">
      <form onSubmit={submit}>
        <div className="border-b p-5">
          <div className="flex items-center gap-3">
            <div className="grid size-10 place-items-center rounded-lg border bg-background"><UserRound className="size-5" /></div>
            <div>
              <h3 className="font-semibold">{user ? 'Edit account' : 'Provision account'}</h3>
              <p className="mt-1 text-xs text-muted">{user ? `Version ${user.version}` : 'Temporary password required'}</p>
            </div>
          </div>
        </div>
        <div className="space-y-4 p-5">
          <Field label="Display name"><input className="field" maxLength={120} onChange={(event) => setDisplayName(event.target.value)} required value={displayName} /></Field>
          <Field label="Email address"><input autoComplete="off" className="field" onChange={(event) => setEmail(event.target.value)} required type="email" value={email} /></Field>
          <Field label={user ? 'New password' : 'Temporary password'} hint={user ? 'Leave empty unless resetting the password.' : '12 to 72 characters. User should rotate it after sign-in.'}>
            <input autoComplete="new-password" className="field" minLength={12} onChange={(event) => setPassword(event.target.value)} required={!user} type="password" value={password} />
          </Field>
          <Field label="Application role" hint="No access removes this application from the account.">
            <select className="field" onChange={(event) => setRoleID(event.target.value)} value={roleID}>
              <option value="">No access</option>
              {overview.roles.map((role) => <option key={role.id} value={role.id}>{role.name}</option>)}
            </select>
          </Field>
          <label className="flex items-start gap-3 rounded-lg border bg-background p-3">
            <input checked={platformAdmin} className="mt-1 size-4 accent-lime-500" disabled={isCurrentUser} onChange={(event) => setPlatformAdmin(event.target.checked)} type="checkbox" />
            <span><span className="block text-sm font-semibold">Platform administrator</span><span className="mt-1 block text-xs leading-5 text-muted">Manage every application, user, and platform setting.</span></span>
          </label>
          {user && (
            <label className="flex items-start gap-3 rounded-lg border bg-background p-3">
              <input checked={enabled} className="mt-1 size-4 accent-lime-500" disabled={isCurrentUser} onChange={(event) => setEnabled(event.target.checked)} type="checkbox" />
              <span><span className="block text-sm font-semibold">Account enabled</span><span className="mt-1 block text-xs leading-5 text-muted">Disabling immediately revokes every active session.</span></span>
            </label>
          )}
          <MutationError error={saveMutation.error ?? passwordMutation.error} />
        </div>
        <div className="flex flex-wrap justify-end gap-2 border-t p-4">
          {user && (
            <Button disabled={password.length < 12 || passwordMutation.isPending} onClick={() => passwordMutation.mutate()} type="button">
              <KeyRound className="size-4" /> Reset password
            </Button>
          )}
          <Button disabled={saveMutation.isPending} type="submit" variant="primary">{saveMutation.isPending ? 'Saving...' : user ? 'Save changes' : 'Create user'}</Button>
        </div>
      </form>
    </Card>
  )
}

function AuditTrail({ logs }: { logs: AccessOverview['auditLogs'] }) {
  return (
    <Card className="overflow-hidden">
      <div className="border-b p-5"><h3 className="font-semibold">Recent audit trail</h3><p className="mt-1 text-xs text-muted">Latest control-plane changes across users and gateway configuration.</p></div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] border-collapse text-left text-sm">
          <thead className="bg-background/60 font-mono text-[10px] tracking-wider text-muted uppercase"><tr><th className="px-5 py-3">Time</th><th className="px-5 py-3">Actor</th><th className="px-5 py-3">Action</th><th className="px-5 py-3">Source</th></tr></thead>
          <tbody className="divide-y">
            {logs.map((log) => <tr key={log.id}><td className="px-5 py-3 font-mono text-[11px] text-muted">{formatDate(log.createdAt)}</td><td className="px-5 py-3 font-medium">{log.actorEmail}</td><td className="px-5 py-3"><Badge>{log.action.replaceAll('.', ' ')}</Badge></td><td className="px-5 py-3 font-mono text-[11px] text-muted">{log.sourceIp || 'local'}</td></tr>)}
            {logs.length === 0 && <tr><td className="px-5 py-8 text-center text-sm text-muted" colSpan={4}>No audited changes yet.</td></tr>}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

function RolePolicy({ roles }: { roles: AccessOverview['roles'] }) {
  return (
    <Card className="overflow-hidden">
      <div className="border-b p-5"><h3 className="font-semibold">Application role policy</h3><p className="mt-1 text-xs text-muted">Role permissions come directly from the database policy.</p></div>
      <div className="grid gap-px bg-border sm:grid-cols-2 xl:grid-cols-4">
        {roles.map((role) => (
          <div className="bg-surface p-5" key={role.id}>
            <p className="font-semibold">{role.name}</p>
            <div className="mt-3 flex flex-wrap gap-1.5">
              {role.permissions.map((permission) => <Badge key={permission}>{permission}</Badge>)}
            </div>
          </div>
        ))}
      </div>
    </Card>
  )
}

function Metric({ icon: Icon, label, value }: { icon: typeof UsersRound; label: string; value: number }) {
  return <div className="flex items-center gap-4 bg-surface p-5"><Icon className="size-5 text-muted" /><div><p className="font-mono text-[10px] font-bold tracking-wider text-muted uppercase">{label}</p><p className="mt-1 text-2xl font-semibold tracking-[-0.04em]">{value}</p></div></div>
}

function Field({ children, hint, label }: { children: ReactNode; hint?: string; label: string }) {
  return <label className="block text-xs font-bold text-muted"><span>{label}</span>{children}<span className="mt-1.5 block font-normal leading-5">{hint}</span></label>
}

function MutationError({ error }: { error: Error | null }) {
  if (!error) return null
  return <p className="rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-danger" role="alert">{error instanceof APIError ? error.message : 'Unable to save account.'}</p>
}

function ErrorState({ error, retry }: { error: Error; retry: () => void }) {
  const message = error instanceof APIError ? error.message : 'Unable to load access control.'
  return <Card className="page-enter p-8"><h2 className="text-xl font-semibold">Access control unavailable</h2><p className="mt-2 text-sm text-danger">{message}</p><Button className="mt-5" onClick={retry}>Retry</Button></Card>
}

function AccessSkeleton() {
  return <div className="space-y-5" aria-label="Loading access control"><div className="h-24 animate-pulse rounded-xl bg-surface" /><div className="grid gap-5 xl:grid-cols-[1.35fr_0.65fr]"><div className="h-96 animate-pulse rounded-xl bg-surface" /><div className="h-96 animate-pulse rounded-xl bg-surface" /></div></div>
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
