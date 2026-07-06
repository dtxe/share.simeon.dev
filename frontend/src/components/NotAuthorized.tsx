import { useLocation } from 'wouter'

export function NotAuthorized() {
  const [, navigate] = useLocation()
  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col items-center justify-center gap-3 p-5 text-center">
      <p className="text-sm text-neutral-600">You don't have access to this bill.</p>
      <button
        type="button"
        onClick={() => navigate('/')}
        className="rounded-lg bg-[var(--color-accent)] px-4 py-2 font-medium text-white"
      >
        Go home
      </button>
    </div>
  )
}
