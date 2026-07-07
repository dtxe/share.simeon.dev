import type { ReactNode } from 'react'
import { ChevronDown, Check } from 'lucide-react'

export function Section({
  title,
  summary,
  complete,
  warn,
  open,
  onToggle,
  children,
}: {
  title: string
  summary?: ReactNode
  complete?: boolean
  warn?: ReactNode
  open: boolean
  onToggle: () => void
  children: ReactNode
}) {
  return (
    <section className="overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-paper)]">
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center justify-between gap-3 px-4 py-3.5 text-left"
        aria-expanded={open}
      >
        <span className="flex items-center gap-2 font-medium">
          {title}
          {complete && !open && <Check size={16} className="text-[var(--color-accent)]" />}
        </span>
        <span className="flex items-center gap-2 text-sm text-[var(--color-ink-soft)]">
          {!open && warn}
          {!open && summary && <span className="font-receipt">{summary}</span>}
          <ChevronDown size={18} className={`transition-transform ${open ? 'rotate-180' : ''}`} />
        </span>
      </button>
      {open && (
        <div className="border-t border-dashed border-[var(--color-border)] px-4 py-4">{children}</div>
      )}
    </section>
  )
}
