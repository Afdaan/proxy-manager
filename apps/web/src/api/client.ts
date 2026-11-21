export interface User {
  id: string
  email: string
  displayName: string
}

export interface FeatureStatus {
  id: string
  name: string
  status: 'foundation' | 'planned' | 'renderer-ready' | 'schema-ready'
}

export interface SystemOverview {
  agentStatus: 'online' | 'offline'
  proxyHosts: number
  certificates: number
  blockedToday: number
  features: FeatureStatus[]
}

export interface Application {
  id: string
  name: string
  slug: string
  canWrite: boolean
}

export interface ProxyHost {
  id: string
  applicationId: string
  application: string
  name: string
  enabled: boolean
  primaryDomain: string
  upstreamUrl: string
  upstreamCount: number
  loadBalancing: 'round_robin' | 'least_conn' | 'ip_hash'
  tlsMode: string
  wafMode: string
  rateLimitEnabled: boolean
  configurationStatus: 'pending' | 'applying' | 'retrying' | 'active' | 'failed'
  configurationError?: string
  version: number
}

export interface UpstreamInput {
  url: string
  weight: number
  maxFails: number
  failAfterSeconds: number
  backup: boolean
}

export interface CreateProxyHostInput {
  applicationId: string
  name: string
  primaryDomain: string
  domainAliases: string[]
  upstreams: UpstreamInput[]
  loadBalancing: ProxyHost['loadBalancing']
  rateLimit: {
    enabled: boolean
    requests: number
    periodSeconds: 1 | 60
    burst: number
    rejectStatus: number
    noDelay: boolean
  }
  waf: {
    mode: 'off' | 'detect' | 'block'
    paranoiaLevel: number
  }
  securityHeaders: boolean
  maxBodySizeMb: number
}

export interface Deployment {
  id: string
  generation: number
  revision: string
  status: 'queued' | 'validating' | 'applying' | 'retrying' | 'succeeded' | 'failed' | 'superseded' | 'rolled_back'
  errorMessage?: string
  startedAt?: string
  finishedAt?: string
  createdAt: string
}

export interface CreateProxyHostResult {
  proxyHost: ProxyHost
  deployment: Deployment
}

export class APIError extends Error {
  status: number
  code: string

  constructor(message: string, status: number, code = 'request_failed') {
    super(message)
    this.status = status
    this.code = code
  }
}

export async function login(email: string, password: string): Promise<User> {
  const result = await request<{ user: User }>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  })
  return result.user
}

export async function logout(): Promise<void> {
  await request<void>('/api/v1/auth/logout', { method: 'POST' })
}

export async function getCurrentUser(): Promise<User> {
  const result = await request<{ user: User }>('/api/v1/auth/me')
  return result.user
}

export function getSystemOverview(): Promise<SystemOverview> {
  return request<SystemOverview>('/api/v1/overview')
}

export async function getApplications(): Promise<Application[]> {
  const result = await request<{ applications: Application[] }>('/api/v1/applications')
  return result.applications
}

export async function getProxyHosts(): Promise<ProxyHost[]> {
  const result = await request<{ proxyHosts: ProxyHost[] }>('/api/v1/proxy-hosts')
  return result.proxyHosts
}

export function createProxyHost(input: CreateProxyHostInput): Promise<CreateProxyHostResult> {
  return request<CreateProxyHostResult>('/api/v1/proxy-hosts', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function getDeployments(): Promise<Deployment[]> {
  const result = await request<{ deployments: Deployment[] }>('/api/v1/deployments')
  return result.deployments
}

export async function createDeployment(): Promise<Deployment> {
  const result = await request<{ deployment: Deployment }>('/api/v1/deployments', { method: 'POST' })
  return result.deployment
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) {
    headers.set('Content-Type', 'application/json')
  }
  if (init.method && !['GET', 'HEAD'].includes(init.method)) {
    const csrfToken = readCookie('proxy_manager_csrf')
    if (csrfToken) {
      headers.set('X-CSRF-Token', csrfToken)
    }
  }

  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    const error = (await response.json().catch(() => null)) as { code?: string; error?: string } | null
    throw new APIError(error?.error ?? `Request failed with ${response.status}`, response.status, error?.code)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return response.json() as Promise<T>
}

function readCookie(name: string): string | undefined {
  const prefix = `${encodeURIComponent(name)}=`
  const cookie = document.cookie.split('; ').find((value) => value.startsWith(prefix))
  return cookie ? decodeURIComponent(cookie.slice(prefix.length)) : undefined
}
