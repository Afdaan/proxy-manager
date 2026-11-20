import type { HTMLAttributes } from 'react'

import { cn } from '../../lib/utils'

interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: 'neutral' | 'success' | 'warning' | 'danger'
}

const tones = {
  neutral: 'border-border bg-surface text-muted',
  success: 'border-lime-500/30 bg-lime-500/10 text-lime-700 dark:text-lime-300',
  warning: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  danger: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300',
}

export function Badge({ className, tone = 'neutral', ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-bold tracking-wide uppercase',
        tones[tone],
        className,
      )}
      {...props}
    />
  )
}
