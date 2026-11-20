import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Network, Plus, Search, X } from 'lucide-react'
import { useMemo, useState, type FormEvent, type ReactNode } from 'react'

import { APIError, createProxyHost, getApplications, getProxyHosts } from '../api/client'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'

export function ProxyHostsPage() {
  const [creating, setCreating] = useState(false)
  const [search, setSearch] = useState('')
  const queryClient = useQueryClient()
  const applicationsQuery = useQuery({ queryKey: ['applications'], queryFn: getApplications })
  const hostsQuery = useQuery({ queryKey: ['proxy-hosts'], queryFn: getProxyHosts })
  const createMutation = useMutation({
    mutationFn: createProxyHost,
    onSuccess: async () => {
      setCreating(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['proxy-hosts'] }),
        queryClient.invalidateQueries({ queryKey: ['overview'] }),
      ])
    },
  })
  const hosts = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return hostsQuery.data ?? []
    return (hostsQuery.data ?? []).filter((host) =>
      [host.name, host.primaryDomain, host.upstreamUrl, host.application].some((value) => value.toLowerCase().includes(query)),
    )
  }, [hostsQuery.data, search])

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    createMutation.mutate({
      applicationId: String(form.get('applicationId') ?? ''),
      name: String(form.get('name') ?? ''),
      primaryDomain: String(form.get('primaryDomain') ?? ''),
      upstreamUrl: String(form.get('upstreamUrl') ?? ''),
    })
  }

  return (
    <div className="page-enter space-y-5">
      <section className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h2 className="text-3xl font-semibold tracking-[-0.04em]">Proxy hosts</h2>
          <p className="mt-2 text-sm text-muted">Domains, upstream targets, certificates, and security posture.</p>
        </div>
        <Button onClick={() => setCreating((value) => !value)} variant="primary">
          {creating ? <X className="size-4" /> : <Plus className="size-4" />} {creating ? 'Cancel' : 'New host'}
        </Button>
      </section>

      {creating && (
        <Card className="p-5">
          <form className="grid gap-4 md:grid-cols-2" onSubmit={submit}>
            <Field label="Application">
              <select className="field" name="applicationId" required>
                {(applicationsQuery.data ?? []).filter((application) => application.canWrite).map((application) => (
                  <option key={application.id} value={application.id}>{application.name}</option>
                ))}
              </select>
            </Field>
            <Field label="Display name">
              <input className="field" name="name" placeholder="Internal dashboard" required />
            </Field>
            <Field label="Domain">
              <input className="field" name="primaryDomain" placeholder="dashboard.example.com" required />
            </Field>
            <Field label="Upstream URL">
              <input className="field" name="upstreamUrl" placeholder="http://10.10.20.15:3000" required type="url" />
            </Field>
            {createMutation.isError && (
              <p className="md:col-span-2 rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-danger" role="alert">
                {createMutation.error instanceof APIError ? createMutation.error.message : 'Unable to create proxy host'}
              </p>
            )}
            <div className="flex justify-end md:col-span-2">
              <Button disabled={createMutation.isPending || applicationsQuery.isPending} type="submit" variant="primary">
                <Plus className="size-4" /> {createMutation.isPending ? 'Creating…' : 'Create proxy host'}
              </Button>
            </div>
          </form>
        </Card>
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
        <div className="hidden grid-cols-[1.4fr_1fr_130px_110px] border-b bg-background/60 px-5 py-3 text-[10px] font-bold tracking-wider text-muted uppercase md:grid">
          <span>Domain</span>
          <span>Upstream</span>
          <span>Protection</span>
          <span>Status</span>
        </div>
        {hostsQuery.isPending ? (
          <div className="grid min-h-72 place-items-center font-mono text-xs text-muted">LOADING HOSTS</div>
        ) : hostsQuery.isError ? (
          <div className="grid min-h-72 place-items-center text-sm text-danger">Unable to load proxy hosts.</div>
        ) : hosts.length ? (
          <div className="divide-y">
            {hosts.map((host) => (
              <div className="grid gap-3 px-5 py-4 md:grid-cols-[1.4fr_1fr_130px_110px] md:items-center" key={host.id}>
                <div>
                  <p className="font-semibold">{host.primaryDomain}</p>
                  <p className="mt-1 text-xs text-muted">{host.application} / {host.name}</p>
                </div>
                <p className="truncate font-mono text-xs text-muted">{host.upstreamUrl}</p>
                <div className="flex gap-2">
                  <Badge>{host.tlsMode}</Badge>
                  <Badge>{host.wafMode}</Badge>
                </div>
                <Badge tone={host.enabled ? 'success' : 'warning'}>{host.enabled ? 'enabled' : 'disabled'}</Badge>
              </div>
            ))}
          </div>
        ) : (
          <div className="grid min-h-72 place-items-center p-8 text-center">
            <div>
              <div className="mx-auto grid size-12 place-items-center rounded-xl border bg-background">
                <Network className="size-5" />
              </div>
              <h3 className="mt-4 font-bold">No proxy hosts yet</h3>
              <p className="mx-auto mt-2 max-w-sm text-sm leading-6 text-muted">
                Create a domain route. Deployment remains separate until its generated configuration passes validation.
              </p>
            </div>
          </div>
        )}
      </Card>
    </div>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="text-sm font-semibold">
      {label}
      <span className="mt-2 block">{children}</span>
    </label>
  )
}
