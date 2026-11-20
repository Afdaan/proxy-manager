import { Ban, Gauge, Globe2, ShieldCheck } from 'lucide-react'

import { Badge } from '../components/ui/badge'
import { Card } from '../components/ui/card'

const modules = [
  {
    icon: Gauge,
    name: 'Rate limiting',
    description: 'Dedicated request zone per proxy host, with burst and 429 controls.',
  },
  {
    icon: ShieldCheck,
    name: 'Web application firewall',
    description: 'ModSecurity and OWASP CRS policy presets: off, detect, and block.',
  },
  {
    icon: Globe2,
    name: 'GeoIP policy',
    description: 'Country allow or block rules using the GeoIP2 country variable.',
  },
  {
    icon: Ban,
    name: 'Network access',
    description: 'Explicit CIDR allowlists and denylists rendered into each virtual host.',
  },
]

export function SecurityPage() {
  return (
    <div className="page-enter space-y-5">
      <div>
        <h2 className="text-3xl font-semibold tracking-[-0.04em]">Security policies</h2>
        <p className="mt-2 max-w-2xl text-sm leading-6 text-muted">
          Small policy surface. No raw NGINX or ModSecurity text from the browser.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {modules.map(({ icon: Icon, name, description }) => (
          <Card className="p-5" key={name}>
            <div className="flex items-start justify-between gap-4">
              <div className="grid size-10 place-items-center rounded-lg border bg-background">
                <Icon className="size-5" />
              </div>
              <Badge tone="success">Renderer ready</Badge>
            </div>
            <h3 className="mt-6 font-bold">{name}</h3>
            <p className="mt-2 text-sm leading-6 text-muted">{description}</p>
          </Card>
        ))}
      </div>
      <Card className="border-amber-500/30 bg-amber-500/5 p-5">
        <p className="text-sm font-bold">Network limit</p>
        <p className="mt-2 text-sm leading-6 text-muted">
          Gateway controls reduce application-layer abuse. They cannot recover bandwidth already saturated before traffic reaches NGINX.
        </p>
      </Card>
    </div>
  )
}
