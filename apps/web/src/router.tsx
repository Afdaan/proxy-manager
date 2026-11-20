import { createRootRoute, createRoute, createRouter } from '@tanstack/react-router'

import { AppShell } from './components/app-shell'
import { AccessPage } from './pages/access'
import { DashboardPage } from './pages/dashboard'
import { ProxyHostsPage } from './pages/proxy-hosts'
import { SecurityPage } from './pages/security'

const rootRoute = createRootRoute({ component: AppShell })
const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: DashboardPage })
const proxyHostsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/proxy-hosts', component: ProxyHostsPage })
const securityRoute = createRoute({ getParentRoute: () => rootRoute, path: '/security', component: SecurityPage })
const accessRoute = createRoute({ getParentRoute: () => rootRoute, path: '/access', component: AccessPage })

const routeTree = rootRoute.addChildren([indexRoute, proxyHostsRoute, securityRoute, accessRoute])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
