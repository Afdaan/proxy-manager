import { Check, Minus } from 'lucide-react'

import { Badge } from '../components/ui/badge'
import { Card } from '../components/ui/card'

const permissions = [
  ['View application', true, true, true, true],
  ['Edit proxy hosts and apply', true, true, true, false],
  ['Manage security policy', true, true, false, false],
  ['Retry and rollback', true, true, true, false],
  ['Manage members', true, false, false, false],
] as const

export function AccessPage() {
  return (
    <div className="page-enter space-y-5">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h2 className="text-3xl font-semibold tracking-[-0.04em]">Access control</h2>
          <p className="mt-2 text-sm text-muted">Roles are assigned per application. Platform administration stays separate.</p>
        </div>
        <Badge>Schema ready</Badge>
      </div>
      <Card className="overflow-x-auto">
        <table className="w-full min-w-[720px] border-collapse text-left text-sm">
          <thead className="bg-background/60 text-[10px] tracking-wider text-muted uppercase">
            <tr>
              <th className="px-5 py-3">Permission</th>
              <th className="px-5 py-3">Owner</th>
              <th className="px-5 py-3">Security admin</th>
              <th className="px-5 py-3">Operator</th>
              <th className="px-5 py-3">Viewer</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {permissions.map(([name, ...roles]) => (
              <tr key={name}>
                <td className="px-5 py-4 font-semibold">{name}</td>
                {roles.map((allowed, index) => (
                  <td className="px-5 py-4" key={`${name}-${index}`}>
                    {allowed ? (
                      <Check className="size-4 text-lime-600 dark:text-lime-300" />
                    ) : (
                      <Minus className="size-4 text-muted/50" />
                    )}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
    </div>
  )
}
