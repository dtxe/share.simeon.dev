import type { ReactNode } from 'react'

export function BigActionCard({
  icon,
  label,
  sublabel,
  onClick,
}: {
  icon: ReactNode
  label: string
  sublabel: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-4 rounded-xl border border-[var(--color-border)] bg-white px-5 py-4 text-left shadow-sm transition active:scale-[0.99] active:bg-[var(--color-accent-tint)]"
    >
      <span className="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-[var(--color-accent-tint)] text-[var(--color-accent)]">
        {icon}
      </span>
      <span>
        <span className="block text-base font-medium">{label}</span>
        <span className="block text-sm text-neutral-500">{sublabel}</span>
      </span>
    </button>
  )
}
