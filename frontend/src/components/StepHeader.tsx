import { useLocation } from 'wouter'
import { ChevronLeft } from 'lucide-react'

const STEPS = ['people', 'items', 'assign', 'results'] as const
type Step = (typeof STEPS)[number]

export function StepHeader({
  sessionId,
  step,
  title,
}: {
  sessionId: string
  step: Step
  title?: string
}) {
  const [, navigate] = useLocation()
  const currentIdx = STEPS.indexOf(step)

  return (
    <header className="sticky top-0 z-10 flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-bg)]/95 px-4 py-3 backdrop-blur">
      <button
        type="button"
        onClick={() => (window.history.length > 1 ? window.history.back() : navigate('/'))}
        aria-label="Back"
        className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-neutral-500"
      >
        <ChevronLeft size={20} />
      </button>
      <span className="flex-1 truncate font-medium">{title ?? 'Untitled bill'}</span>
      <nav className="flex shrink-0 gap-1.5" aria-label="Steps">
        {STEPS.map((s, i) => (
          <button
            key={s}
            type="button"
            aria-label={s}
            aria-current={s === step}
            onClick={() => navigate(`/bill/${sessionId}/${s}`)}
            className="h-2 w-2 rounded-full"
            style={{
              background: i <= currentIdx ? 'var(--color-accent)' : 'var(--color-border)',
            }}
          />
        ))}
      </nav>
    </header>
  )
}
