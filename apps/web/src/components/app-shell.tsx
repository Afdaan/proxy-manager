import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { Activity, Boxes, LogOut, Network, ShieldCheck, UsersRound } from 'lucide-react'

import { APIError, getCurrentUser, logout } from '../api/client'
import { cn } from '../lib/utils'
import { LoginPage } from '../pages/login'
import { ThemeToggle } from './theme-toggle'
import { Badge } from './ui/badge'
import { Button } from './ui/button'

const navigation = [
  { to: '/', label: 'Overview', icon: Activity },
  { to: '/proxy-hosts', label: 'Proxy hosts', icon: Network },
  { to: '/security', label: 'Security', icon: ShieldCheck },
  { to: '/access', label: 'Access', icon: UsersRound },
] as const

const pageNames: Record<string, string> = {
  '/': 'Overview',
  '/proxy-hosts': 'Proxy hosts',
  '/security': 'Security policies',
  '/access': 'Access control',
}

export function AppShell() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const sessionQuery = useQuery({
    queryKey: ['session'],
    queryFn: getCurrentUser,
    retry: false,
  })
  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => window.location.reload(),
  })

  if (sessionQuery.isPending) {
    return <div className="grid min-h-screen place-items-center font-mono text-xs text-muted">CHECKING SESSION</div>
  }
  if (sessionQuery.error instanceof APIError && sessionQuery.error.status === 401) {
    return <LoginPage />
  }
  if (sessionQuery.isError) {
    return <div className="grid min-h-screen place-items-center text-sm text-danger">Control plane unavailable.</div>
  }

  return (
    <div className="min-h-screen lg:grid lg:grid-cols-[240px_1fr]">
      <aside className="hidden border-r bg-surface/95 lg:flex lg:flex-col">
        <Brand />
        <nav className="flex-1 space-y-1 px-3 py-5" aria-label="Primary navigation">
          {navigation.map(({ to, label, icon: Icon }) => (
            <Link
              key={to}
              to={to}
              activeOptions={{ exact: to === '/' }}
              className="flex h-10 items-center gap-3 rounded-md px-3 text-sm font-semibold text-muted transition-colors hover:bg-surface-strong hover:text-foreground"
              activeProps={{ className: 'bg-surface-strong text-foreground shadow-sm' }}
            >
              <Icon className="size-4" />
              {label}
            </Link>
          ))}
        </nav>
        <div className="border-t p-4">
          <div className="rounded-lg border bg-background p-3">
            <div className="flex items-center justify-between">
              <span className="text-xs font-bold">Gateway</span>
              <Badge>Local</Badge>
            </div>
            <p className="mt-2 font-mono text-[11px] text-muted">127.0.0.1:8080</p>
          </div>
        </div>
      </aside>

      <div className="min-w-0">
        <header className="sticky top-0 z-20 flex h-16 items-center justify-between border-b bg-background/90 px-4 backdrop-blur md:px-8">
          <div className="flex items-center gap-3">
            <div className="lg:hidden">
              <Brand compact />
            </div>
            <div>
              <p className="text-xs font-bold tracking-[0.16em] text-muted uppercase">Control plane</p>
              <h1 className="text-base font-bold">{pageNames[pathname] ?? 'Proxy Manager'}</h1>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden text-xs font-semibold text-muted sm:block">{sessionQuery.data.displayName}</span>
            <ThemeToggle />
            <Button
              aria-label="Sign out"
              disabled={logoutMutation.isPending}
              onClick={() => logoutMutation.mutate()}
              variant="ghost"
            >
              <LogOut className="size-4" />
            </Button>
          </div>
        </header>

        <main className="mx-auto max-w-[1440px] px-4 py-6 pb-24 md:px-8 md:py-8 lg:pb-8">
          <Outlet />
        </main>

        <nav
          className="fixed inset-x-3 bottom-3 z-30 grid grid-cols-4 rounded-xl border bg-surface/95 p-1 shadow-xl backdrop-blur lg:hidden"
          aria-label="Mobile navigation"
        >
          {navigation.map(({ to, label, icon: Icon }) => (
            <Link
              key={to}
              to={to}
              activeOptions={{ exact: to === '/' }}
              className="flex min-h-12 flex-col items-center justify-center gap-1 rounded-lg text-[10px] font-bold text-muted"
              activeProps={{ className: 'bg-surface-strong text-foreground' }}
            >
              <Icon className="size-4" />
              {label}
            </Link>
          ))}
        </nav>
      </div>
    </div>
  )
}

function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className={cn('flex h-16 items-center gap-3 border-b px-5', compact && 'h-auto border-0 px-0')}>
      <div className="grid size-9 place-items-center rounded-lg bg-foreground text-background">
        <Boxes className="size-5" />
      </div>
      {!compact && (
        <div>
          <p className="font-bold leading-none">Proxy Manager</p>
          <p className="mt-1 font-mono text-[10px] text-muted">EDGE CONTROL / 0.1</p>
        </div>
      )}
    </div>
  )
}
