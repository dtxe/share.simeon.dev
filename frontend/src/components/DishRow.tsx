import { Minus, Plus } from 'lucide-react'
import type { Dish, Person } from '../lib/api'
import { formatCents } from '../lib/split'
import { personColor, initials } from '../lib/colors'

interface Props {
  dish: Dish
  people: Person[]
  sharesByPerson: Record<string, number>
  totalShares: number
  selected: boolean
  /** Set when a person (not a dish) is selected — this row grows an inline stepper for them. */
  activePersonForStepper: Person | null
  onSelect: () => void
  onAdjust: (personId: string, delta: number) => void
}

export function DishRow({
  dish,
  people,
  sharesByPerson,
  totalShares,
  selected,
  activePersonForStepper,
  onSelect,
  onAdjust,
}: Props) {
  const lineTotal = Math.round(dish.unitPriceCents * dish.quantity)
  const assignedPeople = people.filter((p) => (sharesByPerson[p.id] ?? 0) > 0)

  return (
    <div
      className="flex flex-col gap-2 border-b border-[var(--color-border)] px-4 py-3 last:border-b-0"
      style={{ background: selected ? 'var(--color-accent-tint)' : undefined }}
    >
      <button
        type="button"
        aria-selected={selected}
        onClick={onSelect}
        className="flex w-full items-center justify-between text-left"
        style={selected ? { boxShadow: 'inset 2px 0 0 var(--color-accent)' } : undefined}
      >
        <span className="min-w-0 flex-1 truncate font-medium">
          {dish.quantity > 1 ? `${dish.quantity}× ` : ''}
          {dish.name}
        </span>
        <span className="tabular-nums shrink-0 pl-2">{formatCents(lineTotal)}</span>
      </button>

      {activePersonForStepper ? (
        <div className="flex items-center justify-between rounded-lg bg-white/60 px-2 py-1">
          <span className="text-sm text-neutral-500">Share for {activePersonForStepper.name}</span>
          <div className="flex items-center gap-3">
            <button
              type="button"
              aria-label="Decrease share"
              onClick={() => onAdjust(activePersonForStepper.id, -1)}
              className="flex h-9 w-9 items-center justify-center rounded-full border border-[var(--color-border)] bg-white"
            >
              <Minus size={16} />
            </button>
            <span className="w-6 text-center tabular-nums font-medium">
              {sharesByPerson[activePersonForStepper.id] ?? 0}
            </span>
            <button
              type="button"
              aria-label="Increase share"
              onClick={() => onAdjust(activePersonForStepper.id, 1)}
              className="flex h-9 w-9 items-center justify-center rounded-full border border-[var(--color-border)] bg-white"
            >
              <Plus size={16} />
            </button>
          </div>
        </div>
      ) : assignedPeople.length > 0 ? (
        <div className="flex items-center gap-1">
          {assignedPeople.map((p) => (
            <span
              key={p.id}
              className="flex h-5 w-5 items-center justify-center rounded-full text-[9px] font-bold text-white"
              style={{ background: personColor(p.sortOrder) }}
              title={`${p.name}: ${sharesByPerson[p.id]} share(s)`}
            >
              {initials(p.name)}
            </span>
          ))}
          <span className="text-xs text-neutral-400">{totalShares} share{totalShares === 1 ? '' : 's'}</span>
        </div>
      ) : (
        <span className="flex items-center gap-1 text-xs text-[var(--color-warn)]">
          <span className="h-2 w-2 rounded-full bg-[var(--color-warn)]" /> unassigned
        </span>
      )}
    </div>
  )
}
