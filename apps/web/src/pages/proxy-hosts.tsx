import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { ChevronDown, Lock, Network, Pencil, Plus, Search, Server, ShieldCheck, Trash2, X } from 'lucide-react'
import { useMemo, useState, type FormEvent, type ReactNode } from 'react'

import {
  APIError,
  createProxyHost,
  deleteProxyHost,
  getApplications,
  getProxyHost,
  getProxyHosts,
  requestAutomaticCertificate,
  updateProxyHost,
  type CreateProxyHostResult,
  type CreateProxyHostInput,
  type ProxyHost,
  type ProxyHostDetail,
  type UpdateProxyHostInput,
  type UpstreamInput,
} from '../api/client'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'

const defaultBackend = (): UpstreamInput => ({
  url: '',
  weight: 1,
  maxFails: 3,
  failAfterSeconds: 10,
  backup: false,
})

type HostFormInput = CreateProxyHostInput & { enabled: boolean }

export function ProxyHostsPage() {
  const [creating, setCreating] = useState(false)
  const [editingHostID, setEditingHostID] = useState('')
  const [lastSave, setLastSave] = useState<CreateProxyHostResult | null>(null)
  const [search, setSearch] = useState('')
  const queryClient = useQueryClient()
  const applicationsQuery = useQuery({ queryKey: ['applications'], queryFn: getApplications })
  const hostsQuery = useQuery({
    queryKey: ['proxy-hosts'],
    queryFn: getProxyHosts,
    refetchInterval: (query) => query.state.data?.some((host) => ['applying', 'retrying'].includes(host.configurationStatus)) ? 3000 : false,
  })
  const detailQuery = useQuery({
    queryKey: ['proxy-host', editingHostID],
    queryFn: () => getProxyHost(editingHostID),
    enabled: editingHostID !== '',
  })
  const createMutation = useMutation({
    mutationFn: createProxyHost,
    onSuccess: async (result) => {
      setCreating(false)
      setLastSave(result)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['proxy-hosts'] }),
        queryClient.invalidateQueries({ queryKey: ['overview'] }),
        queryClient.invalidateQueries({ queryKey: ['deployments'] }),
      ])
    },
  })
  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateProxyHostInput }) => updateProxyHost(id, input),
    onSuccess: async (result) => {
      setEditingHostID('')
      setLastSave(result)
      await invalidateGatewayQueries(queryClient)
    },
  })
  const certificateMutation = useMutation({
    mutationFn: ({ id, email, version }: { id: string; email: string; version: number }) =>
      requestAutomaticCertificate(id, email, version),
    onSuccess: async (result) => {
      setLastSave(result)
      await invalidateGatewayQueries(queryClient)
      await queryClient.invalidateQueries({ queryKey: ['proxy-host', editingHostID] })
    },
  })
  const deleteMutation = useMutation({
    mutationFn: ({ id, version }: { id: string; version: number }) => deleteProxyHost(id, version),
    onSuccess: async () => {
      setEditingHostID('')
      setLastSave(null)
      await invalidateGatewayQueries(queryClient)
    },
  })
  const hosts = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return hostsQuery.data ?? []
    return (hostsQuery.data ?? []).filter((host) =>
      [host.name, host.primaryDomain, host.upstreamUrl, host.application].some((value) => value.toLowerCase().includes(query)),
    )
  }, [hostsQuery.data, search])
  const lastSavedHost = lastSave
    ? hostsQuery.data?.find((host) => host.id === lastSave.proxyHost.id)
    : undefined

  return (
    <div className="page-enter space-y-5">
      <section className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h2 className="text-3xl font-semibold tracking-[-0.04em]">Proxy hosts</h2>
          <p className="mt-2 text-sm text-muted">Save once. The gateway validates and applies the complete NGINX configuration automatically.</p>
        </div>
        <Button onClick={() => { setEditingHostID(''); setCreating((value) => !value) }} variant="primary">
          {creating ? <X className="size-4" /> : <Plus className="size-4" />} {creating ? 'Cancel' : 'New host'}
        </Button>
      </section>

      {lastSave && <SaveResult host={lastSavedHost} result={lastSave} />}

      {creating && (
        <HostForm
          applications={applicationsQuery.data ?? []}
          disabled={createMutation.isPending || applicationsQuery.isPending}
          error={createMutation.error}
          onSubmit={({ enabled: _enabled, ...input }) => createMutation.mutate(input)}
        />
      )}

      {editingHostID && (
        detailQuery.isPending ? (
          <Card className="grid min-h-64 place-items-center font-mono text-xs text-muted">LOADING HOST CONFIGURATION</Card>
        ) : detailQuery.isError ? (
          <Card className="p-5 text-sm text-danger">{detailQuery.error instanceof APIError ? detailQuery.error.message : 'Unable to load proxy host.'}</Card>
        ) : (
          <HostForm
            applications={applicationsQuery.data ?? []}
            disabled={updateMutation.isPending || deleteMutation.isPending || certificateMutation.isPending}
            error={updateMutation.error ?? deleteMutation.error ?? certificateMutation.error}
            host={detailQuery.data}
            onCertificate={(email) => certificateMutation.mutate({ id: detailQuery.data.id, email, version: detailQuery.data.version })}
            onDelete={() => {
              if (window.confirm(`Delete ${detailQuery.data.primaryDomain}? NGINX will remove this route immediately after validation.`)) {
                deleteMutation.mutate({ id: detailQuery.data.id, version: detailQuery.data.version })
              }
            }}
            onSubmit={(input) => updateMutation.mutate({
              id: detailQuery.data.id,
              input: {
                enabled: input.enabled,
                name: input.name,
                primaryDomain: input.primaryDomain,
                domainAliases: input.domainAliases,
                upstreams: input.upstreams,
                loadBalancing: input.loadBalancing,
                rateLimit: input.rateLimit,
                waf: input.waf,
                securityHeaders: input.securityHeaders,
                maxBodySizeMb: input.maxBodySizeMb,
                version: detailQuery.data.version,
              },
            })}
          />
        )
      )}

      <Card className="overflow-hidden">
        <div className="flex items-center gap-3 border-b px-4 py-3">
          <Search className="size-4 text-muted" />
          <input
            aria-label="Search proxy hosts"
            className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted"
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search domain or application"
            value={search}
          />
        </div>
        <div className="hidden grid-cols-[1.4fr_1fr_150px_110px_44px] border-b bg-background/60 px-5 py-3 text-[10px] font-bold tracking-wider text-muted uppercase md:grid">
          <span>Domain</span>
          <span>Backend pool</span>
          <span>Protection</span>
          <span>Status</span>
          <span className="sr-only">Actions</span>
        </div>
        {hostsQuery.isPending ? (
          <div className="grid min-h-72 place-items-center font-mono text-xs text-muted">LOADING HOSTS</div>
        ) : hostsQuery.isError ? (
          <div className="grid min-h-72 place-items-center text-sm text-danger">Unable to load proxy hosts.</div>
        ) : hosts.length ? (
          <div className="divide-y">
            {hosts.map((host) => (
              <div className="grid gap-3 px-5 py-4 md:grid-cols-[1.4fr_1fr_150px_110px_44px] md:items-center" key={host.id}>
                <div>
                  <p className="font-semibold">{host.primaryDomain}</p>
                  <p className="mt-1 text-xs text-muted">{host.application} / {host.name}</p>
                </div>
                <div className="min-w-0">
                  <p className="truncate font-mono text-xs text-muted">{host.upstreamUrl}</p>
                  <p className="mt-1 text-[11px] text-muted">{host.upstreamCount} backend · {host.loadBalancing.replace('_', ' ')}</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Badge>{host.tlsMode}</Badge>
                  {host.wafMode !== 'off' && <Badge tone="success">WAF {host.wafMode}</Badge>}
                  {host.rateLimitEnabled && <Badge tone="success">Rate limit</Badge>}
                </div>
                <ConfigurationBadge host={host} />
                {host.canWrite ? (
                  <Button
                    aria-label={`Edit ${host.primaryDomain}`}
                    onClick={() => { setCreating(false); setEditingHostID((current) => current === host.id ? '' : host.id) }}
                    variant="ghost"
                  >
                    {editingHostID === host.id ? <X className="size-4" /> : <Pencil className="size-4" />}
                  </Button>
                ) : <span />}
              </div>
            ))}
          </div>
        ) : (
          <EmptyHosts />
        )}
      </Card>
    </div>
  )
}

function HostForm({
  applications,
  disabled,
  error,
  host,
  onCertificate,
  onDelete,
  onSubmit,
}: {
  applications: Array<{ id: string; name: string; canWrite: boolean }>
  disabled: boolean
  error: Error | null
  host?: ProxyHostDetail
  onCertificate?: (email: string) => void
  onDelete?: () => void
  onSubmit: (input: HostFormInput) => void
}) {
  const [backends, setBackends] = useState<UpstreamInput[]>(host?.upstreams.length ? host.upstreams : [defaultBackend()])
  const [rateLimitEnabled, setRateLimitEnabled] = useState(host?.rateLimit.enabled ?? false)
  const [wafMode, setWafMode] = useState<'off' | 'detect' | 'block'>(host?.waf.mode ?? 'off')
  const [certEmail, setCertEmail] = useState(host?.tls.email ?? '')

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    onSubmit({
      applicationId: host?.applicationId ?? String(form.get('applicationId') ?? ''),
      enabled: form.get('enabled') === 'on',
      name: String(form.get('name') ?? ''),
      primaryDomain: String(form.get('primaryDomain') ?? ''),
      domainAliases: String(form.get('domainAliases') ?? '').split(/[\s,]+/).filter(Boolean),
      upstreams: backends,
      loadBalancing: String(form.get('loadBalancing') ?? 'round_robin') as CreateProxyHostInput['loadBalancing'],
      rateLimit: {
        enabled: rateLimitEnabled,
        requests: Number(form.get('rateRequests') ?? host?.rateLimit.requests ?? 60),
        periodSeconds: Number(form.get('ratePeriod') ?? host?.rateLimit.periodSeconds ?? 60) as 1 | 60,
        burst: Number(form.get('rateBurst') ?? host?.rateLimit.burst ?? 20),
        rejectStatus: 429,
        noDelay: true,
      },
      waf: {
        mode: wafMode,
        paranoiaLevel: Number(form.get('wafParanoia') ?? host?.waf.paranoiaLevel ?? 1),
      },
      securityHeaders: form.get('securityHeaders') === 'on',
      maxBodySizeMb: Number(form.get('maxBodySizeMb') ?? 10),
    })
  }

  function updateBackend(index: number, patch: Partial<UpstreamInput>) {
    setBackends((current) => current.map((backend, backendIndex) => backendIndex === index ? { ...backend, ...patch } : backend))
  }

  return (
    <Card className="overflow-hidden">
      <form onSubmit={submit}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label="Application">
            <select className="field" defaultValue={host?.applicationId} disabled={Boolean(host)} name="applicationId" required>
              {applications.filter((application) => application.canWrite).map((application) => (
                <option key={application.id} value={application.id}>{application.name}</option>
              ))}
            </select>
          </Field>
          <Field label="Display name">
            <input className="field" defaultValue={host?.name} name="name" placeholder="Internal dashboard" required />
          </Field>
          <Field label="Primary domain">
            <input className="field" defaultValue={host?.primaryDomain} name="primaryDomain" placeholder="dashboard.example.com" required />
          </Field>
          <Field label="Backend URL">
            <input
              className="field font-mono text-sm"
              onChange={(event) => updateBackend(0, { url: event.target.value })}
              placeholder="http://10.10.20.15:3000"
              required
              type="url"
              value={backends[0].url}
            />
          </Field>
        </div>

        <details className="group border-t" open={Boolean(host)}>
          <summary className="flex cursor-pointer list-none items-center justify-between px-5 py-4 text-sm font-bold">
            <span className="flex items-center gap-2"><ShieldCheck className="size-4" /> Advanced routing and protection</span>
            <ChevronDown className="size-4 text-muted transition-transform group-open:rotate-180" />
          </summary>
          <div className="space-y-6 border-t bg-background/35 p-5">
            <section>
              <SectionTitle icon={Server} title="Backend pool" description="Add replicas only when the application has more than one reachable backend." />
              <div className="mt-4 space-y-3">
                {backends.map((backend, index) => (
                  <div className="grid gap-3 rounded-lg border bg-surface p-3 md:grid-cols-[1fr_100px_110px_auto] md:items-end" key={index}>
                    <Field label={index === 0 ? 'Primary backend' : `Backend ${index + 1}`}>
                      <input className="field font-mono text-sm" onChange={(event) => updateBackend(index, { url: event.target.value })} required type="url" value={backend.url} />
                    </Field>
                    <Field label="Weight">
                      <input className="field" max="100" min="1" onChange={(event) => updateBackend(index, { weight: Number(event.target.value) })} type="number" value={backend.weight} />
                    </Field>
                    <label className="flex h-10 items-center gap-2 text-sm font-semibold">
                      <input checked={backend.backup} disabled={backends.length === 1} onChange={(event) => updateBackend(index, { backup: event.target.checked })} type="checkbox" /> Backup only
                    </label>
                    {index > 0 && (
                      <Button aria-label={`Remove backend ${index + 1}`} onClick={() => setBackends((current) => current.filter((_, backendIndex) => backendIndex !== index))} type="button" variant="ghost">
                        <Trash2 className="size-4" />
                      </Button>
                    )}
                  </div>
                ))}
              </div>
              <div className="mt-3 flex flex-wrap items-end gap-3">
                <Button onClick={() => setBackends((current) => [...current, defaultBackend()])} type="button"><Plus className="size-4" /> Add backend</Button>
                <Field label="Load balancing">
                  <select className="field min-w-44" defaultValue={host?.loadBalancing ?? 'round_robin'} name="loadBalancing">
                    <option value="round_robin">Round robin</option>
                    <option value="least_conn">Least connections</option>
                    <option value="ip_hash">IP hash</option>
                  </select>
                </Field>
              </div>
            </section>

            <section className="grid gap-4 border-t pt-6 md:grid-cols-2">
              <Field label="Domain aliases">
                <textarea className="field min-h-24 py-2" defaultValue={host?.domainAliases.join(', ')} name="domainAliases" placeholder="www.example.com, api.example.com" />
              </Field>
              <Field label="Maximum request body">
                <div className="relative">
                  <input className="field pr-12" defaultValue={host?.maxBodySizeMb ?? 10} max="10240" min="1" name="maxBodySizeMb" type="number" />
                  <span className="pointer-events-none absolute inset-y-0 right-3 grid place-items-center text-xs text-muted">MB</span>
                </div>
              </Field>
            </section>

            <section className="grid gap-4 border-t pt-6 md:grid-cols-2">
              <div className="rounded-lg border bg-surface p-4">
                <label className="flex items-center justify-between gap-4 text-sm font-bold">
                  Rate limiting
                  <input checked={rateLimitEnabled} onChange={(event) => setRateLimitEnabled(event.target.checked)} type="checkbox" />
                </label>
                <div className="mt-4 grid grid-cols-3 gap-3">
                  <Field label="Requests"><input className="field" defaultValue={host?.rateLimit.requests || 60} disabled={!rateLimitEnabled} min="1" name="rateRequests" type="number" /></Field>
                  <Field label="Period"><select className="field" defaultValue={host?.rateLimit.periodSeconds || 60} disabled={!rateLimitEnabled} name="ratePeriod"><option value="60">Minute</option><option value="1">Second</option></select></Field>
                  <Field label="Burst"><input className="field" defaultValue={host?.rateLimit.burst ?? 20} disabled={!rateLimitEnabled} min="0" name="rateBurst" type="number" /></Field>
                </div>
              </div>
              <div className="rounded-lg border bg-surface p-4">
                <Field label="Web application firewall">
                  <select className="field" onChange={(event) => setWafMode(event.target.value as typeof wafMode)} value={wafMode}>
                    <option value="off">Off</option>
                    <option value="detect">Detect only</option>
                    <option value="block">Block requests</option>
                  </select>
                </Field>
                <div className="mt-3">
                  <p className="text-xs leading-5 text-muted">Detection and blocking use the managed OWASP CRS baseline installed on the gateway.</p>
                </div>
                {wafMode !== 'off' && <div className="mt-3"><Field label="Paranoia level"><input className="field" defaultValue={host?.waf.paranoiaLevel || 1} max="4" min="1" name="wafParanoia" type="number" /></Field></div>}
              </div>
            </section>

            <div className="flex flex-wrap gap-5">
            {host && (
              <section className="rounded-lg border bg-surface p-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <p className="flex items-center gap-2 font-bold text-sm"><Lock className="size-4" /> Automatic TLS certificate</p>
                    <p className="mt-1 text-xs text-muted">
                      {host.tls.ready
                        ? 'Certificate active. NGINX listens on 443 with HTTP to HTTPS redirect.'
                        : host.tls.error
                          ? `Last issue attempt failed: ${host.tls.error}`
                          : 'Issue a Let’s Encrypt certificate after DNS points to this server.'}
                    </p>
                  </div>
                  <div className="flex items-center gap-2">
                    <input
                      className="field min-w-56"
                      disabled={disabled}
                      onChange={(event) => setCertEmail(event.target.value)}
                      placeholder="admin@example.com"
                      type="email"
                      value={certEmail}
                    />
                    <Button
                      disabled={disabled || !certEmail}
                      onClick={() => onCertificate?.(certEmail)}
                      type="button"
                      variant="secondary"
                    >
                      {host.tls.ready ? 'Renew / re-issue' : 'Request certificate'}
                    </Button>
                  </div>
                </div>
              </section>
            )}

              <label className="flex items-center gap-3 text-sm font-semibold">
                <input defaultChecked={host?.securityHeaders ?? true} name="securityHeaders" type="checkbox" /> Add safe security headers
              </label>
              <label className="flex items-center gap-3 text-sm font-semibold">
                <input defaultChecked={host?.enabled ?? true} name="enabled" type="checkbox" /> Route enabled
              </label>
            </div>
          </div>
        </details>

        <div className="flex flex-col gap-3 border-t px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-xs text-muted">Save validates the generated configuration and applies it without interrupting existing traffic.</p>
          <div className="flex items-center justify-end gap-3">
            {error && <p className="text-sm text-danger" role="alert">{error instanceof APIError ? error.message : 'Unable to save proxy host'}</p>}
            {host && <Button disabled={disabled} onClick={onDelete} type="button"><Trash2 className="size-4" /> Delete</Button>}
            <Button disabled={disabled} type="submit" variant="primary">{host ? <Pencil className="size-4" /> : <Plus className="size-4" />} {disabled ? 'Saving & applying…' : host ? 'Save & apply' : 'Create & apply'}</Button>
          </div>
        </div>
      </form>
    </Card>
  )
}

function SectionTitle({ icon: Icon, title, description }: { icon: typeof Server; title: string; description: string }) {
  return (
    <div className="flex gap-3">
      <div className="grid size-9 shrink-0 place-items-center rounded-md border bg-surface"><Icon className="size-4" /></div>
      <div><h3 className="text-sm font-bold">{title}</h3><p className="mt-1 text-xs leading-5 text-muted">{description}</p></div>
    </div>
  )
}

function EmptyHosts() {
  return (
    <div className="grid min-h-72 place-items-center p-8 text-center">
      <div>
        <div className="mx-auto grid size-12 place-items-center rounded-xl border bg-background"><Network className="size-5" /></div>
        <h3 className="mt-4 font-bold">No proxy hosts yet</h3>
        <p className="mx-auto mt-2 max-w-sm text-sm leading-6 text-muted">Create a domain route. Its generated NGINX configuration is validated and applied automatically.</p>
      </div>
    </div>
  )
}

function SaveResult({ host, result }: { host?: ProxyHost; result: CreateProxyHostResult }) {
  const currentHost = host ?? result.proxyHost
  const status = currentHost.configurationStatus
  const failed = status === 'failed'
  const waiting = status === 'retrying' || status === 'applying'
  return (
    <Card className={`border-l-4 p-4 ${failed ? 'border-l-red-500' : waiting ? 'border-l-amber-400' : 'border-l-emerald-400'}`}>
      <p className="text-sm font-bold">
        {failed ? 'Host saved, configuration rejected' : waiting ? 'Host saved, gateway reconciliation continues' : 'Host active'}
      </p>
      <p className="mt-1 text-xs leading-5 text-muted">
        {failed
          ? currentHost.configurationError ?? result.deployment.errorMessage
          : waiting
            ? 'Existing traffic remains on the last valid revision. The system retries temporary gateway failures automatically.'
            : `${currentHost.primaryDomain} is active on the gateway.`}
      </p>
    </Card>
  )
}

function ConfigurationBadge({ host }: { host: ProxyHost }) {
  const labels: Record<ProxyHost['configurationStatus'], string> = {
    active: 'active',
    applying: 'applying',
    failed: 'failed',
    pending: 'pending',
    retrying: 'retrying',
  }
  const tone = host.configurationStatus === 'active' ? 'success' : host.configurationStatus === 'failed' ? 'danger' : 'warning'
  return <Badge title={host.configurationError} tone={tone}>{labels[host.configurationStatus]}</Badge>
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="text-sm font-semibold">{label}<span className="mt-2 block">{children}</span></label>
}

async function invalidateGatewayQueries(queryClient: QueryClient) {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: ['proxy-hosts'] }),
    queryClient.invalidateQueries({ queryKey: ['overview'] }),
    queryClient.invalidateQueries({ queryKey: ['deployments'] }),
  ])
}
