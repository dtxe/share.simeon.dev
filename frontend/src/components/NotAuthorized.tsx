import { useLocation } from 'wouter'

export function NotAuthorized() {
  const [, navigate] = useLocation()
  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col items-center justify-center gap-3 p-5 text-center">
      <p className="text-sm text-foreground-muted">You don't have access to this bill.</p>
      <button
        type="button"
        onClick={() => navigate('/')}
        className="rounded-lg bg-primary px-4 py-2 font-medium text-primary-foreground"
      >
        Go home
      </button>
    </div>
  )
}
