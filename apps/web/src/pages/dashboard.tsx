import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  Activity,
  Ban,
  CheckCircle2,
  Clock3,
  KeyRound,
  Network,
  RefreshCw,
  ShieldCheck,
  TerminalSquare,
  XCircle,
} from 'lucide-react'

import { APIError, createDeployment, getDeployments, getSystemOverview, type Deployment, type FeatureStatus } from '../api/client'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'

const emptyOverview = {
  agentStatus: 'offline' as const,
  proxyHosts: 0,
  certificates: 0,
  blockedToday: 0,
  features: [] as FeatureStatus[],
}

const fallbackFeatures: FeatureStatus[] = [
  { id: 'reverse-proxy', name: 'Reverse proxy', status: 'foundation' },
  { id: 'rate-limit', name: 'Rate limiting', status: 'renderer-ready' },
  { id: 'waf', name: 'WAF policy', status: 'renderer-ready' },
]

export function DashboardPage() {
  const queryClient = useQueryClient()
  const overviewQuery = useQuery({ queryKey: ['overview'], queryFn: getSystemOverview })
  const deploymentsQuery = useQuery({
    queryKey: ['deployments'],
    queryFn: getDeployments,
    retry: false,
    refetchInterval: (query) => {
      const status = query.state.data?.[0]?.status
      return status && ['queued', 'validating', 'applying', 'retrying'].includes(status) ? 3000 : false
    },
  })
  const retryMutation = useMutation({
    mutationFn: createDeployment,
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ['deployments'] })
    },
  })
  const overview = overviewQuery.data ?? emptyOverview
  const features = overview.features.length ? overview.features : fallbackFeatures
  const latestDeployment = deploymentsQuery.data?.[0]
  const canDeploy = deploymentsQuery.isSuccess

  return (
    <div className="page-enter space-y-6">
      <section className="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
        <div>
          <p className="font-mono text-xs text-muted">{jakartaDate()}</p>
          <h2 className="mt-2 max-w-3xl text-3xl font-semibold tracking-[-0.04em] md:text-5xl">
            One ingress. Clear control.
          </h2>
          <p className="mt-3 max-w-2xl text-sm leading-6 text-muted">
            Manage NGINX routes, certificates, and security policy without exposing raw infrastructure controls.
          </p>
        </div>
        <Link className="inline-flex h-9 items-center justify-center gap-2 rounded-md bg-accent px-3 text-sm font-semibold text-accent-foreground transition-colors hover:bg-accent/85" to="/proxy-hosts">
          <Network className="size-4" /> Add proxy host
        </Link>
      </section>

      <section className="grid gap-3 sm:grid-cols-3">
        <Metric icon={Network} label="Proxy hosts" value={overview.proxyHosts} />
        <Metric icon={KeyRound} label="Certificates" value={overview.certificates} />
        <Metric icon={Ban} label="Blocked today" value={overview.blockedToday} />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.45fr_1fr]">
        <RuntimeStatus agentStatus={overview.agentStatus} serverOnline={!overviewQuery.isError} />
        <DeploymentPanel
          agentOnline={overview.agentStatus === 'online'}
          canDeploy={canDeploy}
          error={retryMutation.error}
          latest={latestDeployment}
          loading={deploymentsQuery.isPending}
          pending={retryMutation.isPending}
          onRetry={() => retryMutation.mutate()}
        />
      </section>

      <Card>
        <div className="border-b px-5 py-4">
          <h3 className="font-bold">Security modules</h3>
          <p className="mt-1 text-xs text-muted">Capability status from the server contract</p>
        </div>
        <div className="grid divide-y md:grid-cols-2 md:divide-x md:divide-y-0 xl:grid-cols-3">
          {features.map((feature) => (
            <div className="flex items-center justify-between gap-3 p-5" key={feature.id}>
              <div>
                <p className="text-sm font-bold">{feature.name}</p>
                <p className="mt-1 font-mono text-[10px] text-muted">{feature.id}</p>
              </div>
              <Badge tone={feature.status === 'planned' ? 'warning' : 'success'}>{feature.status}</Badge>
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}

function jakartaDate() {
  const parts = new Intl.DateTimeFormat('en-GB', {
    dateStyle: 'full',
    timeZone: 'Asia/Jakarta',
  }).formatToParts(new Date())
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]))
  return `${values.weekday} / ${values.day} ${values.month} ${values.year} / ASIA-JAKARTA`.toUpperCase()
}

function Metric({ icon: Icon, label, value }: { icon: typeof Network; label: string; value: number }) {
  return (
    <Card className="flex items-center justify-between p-5">
      <div>
        <p className="text-xs font-bold tracking-wide text-muted uppercase">{label}</p>
        <p className="mt-3 text-3xl font-semibold tracking-[-0.04em]">{value}</p>
      </div>
      <div className="grid size-11 place-items-center rounded-lg border bg-background">
        <Icon className="size-5" />
      </div>
    </Card>
  )
}

function RuntimeStatus({ agentStatus, serverOnline }: { agentStatus: string; serverOnline: boolean }) {
  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b px-5 py-4">
        <div>
          <h3 className="font-bold">Runtime status</h3>
          <p className="mt-1 text-xs text-muted">Live control-plane components</p>
        </div>
        <Badge tone={agentStatus === 'online' ? 'success' : 'warning'}>{agentStatus}</Badge>
      </div>
      <div className="divide-y">
        <RuntimeRow icon={Activity} name="Web server" note="API and embedded UI" status={serverOnline ? 'online' : 'offline'} />
        <RuntimeRow icon={TerminalSquare} name="Gateway agent" note="NGINX apply and rollback" status={agentStatus} />
        <RuntimeRow icon={ShieldCheck} name="Policy renderer" note="Typed NGINX configuration" status="ready" />
      </div>
    </Card>
  )
}

function RuntimeRow({ icon: Icon, name, note, status }: { icon: typeof Activity; name: string; note: string; status: string }) {
  const online = status === 'online' || status === 'ready'
  return (
    <div className="flex items-center gap-4 px-5 py-4">
      <div className="grid size-9 place-items-center rounded-md border bg-background">
        <Icon className="size-4" />
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-bold">{name}</p>
        <p className="mt-0.5 truncate text-xs text-muted">{note}</p>
      </div>
      <span className={`size-2 rounded-full ${online ? 'bg-lime-500' : 'bg-amber-400'}`} aria-label={status} />
    </div>
  )
}

function DeploymentPanel({
  agentOnline,
  canDeploy,
  error,
  latest,
  loading,
  pending,
  onRetry,
}: {
  agentOnline: boolean
  canDeploy: boolean
  error: Error | null
  latest?: Deployment
  loading: boolean
  pending: boolean
  onRetry: () => void
}) {
  return (
    <Card className="overflow-hidden">
      <div className="flex items-start justify-between gap-4 border-b px-5 py-4">
        <div>
          <h3 className="font-bold">NGINX configuration</h3>
          <p className="mt-1 text-xs leading-5 text-muted">Saved changes reconcile automatically into one validated, immutable revision.</p>
        </div>
        {latest && <DeploymentBadge status={latest.status} />}
      </div>
      <div className="p-5">
        {loading ? (
          <p className="font-mono text-xs text-muted">LOADING CONFIGURATION STATE</p>
        ) : latest ? (
          <div>
            <p className="text-xs font-bold tracking-wide text-muted uppercase">Latest revision</p>
            <p className="mt-2 break-all font-mono text-xs">{latest.revision}</p>
            <p className="mt-2 text-xs text-muted">Generation {latest.generation} · {formatDeploymentTime(latest.createdAt)}</p>
            {latest.errorMessage && <p className="mt-3 rounded-md border border-red-500/30 bg-red-500/10 p-3 text-xs leading-5 text-danger">{latest.errorMessage}</p>}
          </div>
        ) : (
          <p className="text-sm leading-6 text-muted">No configuration revision exists yet. Creating a proxy host will apply the first revision automatically.</p>
        )}
        {error && (
          <p className="mt-3 text-xs text-danger" role="alert">
            {error instanceof APIError ? error.message : 'Configuration retry failed'}
          </p>
        )}
        {latest?.status === 'failed' && (
          <Button className="mt-5 w-full" disabled={!agentOnline || !canDeploy || pending} onClick={onRetry} variant="primary">
            <RefreshCw className="size-4" /> {pending ? 'Retrying…' : 'Retry configuration'}
          </Button>
        )}
        {latest?.status === 'retrying' && <p className="mt-4 text-xs leading-5 text-muted">The gateway is temporarily unavailable. Retry runs automatically while the last valid revision remains active.</p>}
        {latest?.status === 'failed' && !agentOnline && <p className="mt-2 text-center text-[11px] text-muted">Gateway agent must be online before retrying.</p>}
        {latest?.status === 'failed' && !canDeploy && <p className="mt-2 text-center text-[11px] text-muted">Your role cannot retry gateway configuration.</p>}
      </div>
    </Card>
  )
}

function DeploymentBadge({ status }: { status: Deployment['status'] }) {
  const tone = status === 'succeeded' ? 'success' : status === 'failed' ? 'danger' : 'warning'
  const Icon = status === 'succeeded' ? CheckCircle2 : status === 'failed' ? XCircle : Clock3
  return <Badge tone={tone}><Icon className="mr-1 size-3" /> {status.replace('_', ' ')}</Badge>
}

function formatDeploymentTime(value: string) {
  return new Intl.DateTimeFormat('en-GB', {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'Asia/Jakarta',
  }).format(new Date(value))
}
