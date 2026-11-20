import { Moon, Sun } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from './ui/button'

type Theme = 'light' | 'dark'

function initialTheme(): Theme {
  const saved = localStorage.getItem('theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(initialTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('theme', theme)
  }, [theme])

  const nextTheme = theme === 'dark' ? 'light' : 'dark'
  const Icon = theme === 'dark' ? Sun : Moon

  return (
    <Button
      aria-label={`Switch to ${nextTheme} theme`}
      className="size-9 px-0"
      onClick={() => setTheme(nextTheme)}
      variant="ghost"
    >
      <Icon className="size-4" />
    </Button>
  )
}
