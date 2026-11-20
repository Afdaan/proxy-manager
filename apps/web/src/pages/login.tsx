import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Boxes, LogIn } from 'lucide-react'
import type { FormEvent } from 'react'

import { APIError, login } from '../api/client'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'

export function LoginPage() {
  const queryClient = useQueryClient()
  const loginMutation = useMutation({
    mutationFn: ({ email, password }: { email: string; password: string }) => login(email, password),
    onSuccess: (user) => queryClient.setQueryData(['session'], user),
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    loginMutation.mutate({
      email: String(form.get('email') ?? ''),
      password: String(form.get('password') ?? ''),
    })
  }

  return (
    <main className="grid min-h-screen place-items-center px-4 py-10">
      <Card className="page-enter w-full max-w-md overflow-hidden">
        <div className="border-b p-6">
          <div className="grid size-11 place-items-center rounded-lg bg-foreground text-background">
            <Boxes className="size-6" />
          </div>
          <p className="mt-8 font-mono text-[10px] tracking-[0.18em] text-muted uppercase">Edge control / restricted</p>
          <h1 className="mt-2 text-3xl font-semibold tracking-[-0.04em]">Sign in to the gateway</h1>
          <p className="mt-3 text-sm leading-6 text-muted">Use the administrator account configured during first startup.</p>
        </div>
        <form className="space-y-4 p-6" onSubmit={submit}>
          <label className="block text-sm font-semibold">
            Email
            <input
              autoComplete="username"
              className="mt-2 h-10 w-full rounded-md border bg-background px-3 outline-none focus:ring-2 focus:ring-accent"
              name="email"
              required
              type="email"
            />
          </label>
          <label className="block text-sm font-semibold">
            Password
            <input
              autoComplete="current-password"
              className="mt-2 h-10 w-full rounded-md border bg-background px-3 outline-none focus:ring-2 focus:ring-accent"
              minLength={12}
              name="password"
              required
              type="password"
            />
          </label>
          {loginMutation.isError && (
            <p className="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-danger" role="alert">
              {loginMutation.error instanceof APIError ? loginMutation.error.message : 'Sign in failed'}
            </p>
          )}
          <Button className="w-full" disabled={loginMutation.isPending} type="submit" variant="primary">
            <LogIn className="size-4" /> {loginMutation.isPending ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>
      </Card>
    </main>
  )
}
