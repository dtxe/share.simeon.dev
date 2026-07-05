import { Minus } from 'lucide-react'
import { personColor, initials } from '../lib/colors'
import { formatCents } from '../lib/split'

interface Props {
  name: string
  colorIndex: number
  /** When a dish is selected, this chip shows that dish's share count for this person and becomes tap-to-assign. */
  shareCount?: number
  /** Idle-mode caption under the chip: running owed total, in cents. */
  owedCents?: number
  selected?: boolean
  onTap: () => void
  onDecrement?: () => void
}

export function PersonChip({ name, colorIndex, shareCount, owedCents, selected, onTap, onDecrement }: Props) {
  const filled = (shareCount ?? 0) > 0
  return (
    <div className="relative flex shrink-0 flex-col items-center gap-1">
      {onDecrement && filled && (
        <button
          type="button"
          aria-label={`Decrease ${name}'s share`}
          onClick={onDecrement}
          className="absolute -left-1 -top-1 z-10 flex h-6 w-6 items-center justify-center rounded-full border border-[var(--color-border)] bg-white shadow-sm"
        >
          <Minus size={12} />
        </button>
      )}
      <button
        type="button"
        aria-pressed={selected}
        aria-label={
          shareCount !== undefined ? `${name}, ${shareCount} shares` : `Select ${name}`
        }
        onClick={onTap}
        className="relative flex h-12 w-12 items-center justify-center rounded-full text-sm font-semibold text-white ring-offset-2"
        style={{
          background: filled || selected ? personColor(colorIndex) : 'white',
          color: filled || selected ? 'white' : personColor(colorIndex),
          border: `2px solid ${personColor(colorIndex)}`,
          boxShadow: selected ? `0 0 0 2px ${personColor(colorIndex)}` : undefined,
        }}
      >
        {initials(name)}
        {shareCount !== undefined && shareCount > 0 && (
          <span className="absolute -right-1 -top-1 flex h-5 w-5 items-center justify-center rounded-full bg-[var(--color-ink)] text-[10px] font-bold text-white">
            {shareCount}
          </span>
        )}
      </button>
      <span className="max-w-14 truncate text-[11px] text-neutral-600">{name}</span>
      {owedCents !== undefined && (
        <span className="tabular-nums text-[11px] font-medium">{formatCents(owedCents)}</span>
      )}
    </div>
  )
}
