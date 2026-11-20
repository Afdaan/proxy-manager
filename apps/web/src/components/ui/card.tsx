import type { HTMLAttributes } from 'react'

import { cn } from '../../lib/utils'

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <section
      className={cn('rounded-xl border bg-surface shadow-[0_1px_0_rgb(0_0_0/0.03)]', className)}
      {...props}
    />
  )
}
