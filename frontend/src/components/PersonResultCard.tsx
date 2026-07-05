import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import type { Dish, Person, Portion } from '../lib/api'
import { personColor, initials } from '../lib/colors'
import { formatCents } from '../lib/split'

export function PersonResultCard({
  person,
  owedCents,
  dishes,
  portions,
  subtotalCents,
  totalPaidCents,
  defaultOpen,
}: {
  person: Person
  owedCents: number
  dishes: Dish[]
  portions: Portion[]
  /** Used to scale each line item by the same tip/discount factor as the
   * final total, so displayed lines sum to the header amount instead of
   * only ever reflecting the pre-tip subtotal share. */
  subtotalCents: number
  totalPaidCents: number | null
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(!!defaultOpen)
  const scale = subtotalCents > 0 ? (totalPaidCents ?? subtotalCents) / subtotalCents : 1

  const lines = dishes
    .map((d) => {
      const mine = portions.find((p) => p.dishId === d.id && p.personId === person.id)?.shares ?? 0
      const total = portions.filter((p) => p.dishId === d.id).reduce((s, p) => s + p.shares, 0)
      if (mine <= 0 || total <= 0) return null
      const dishValue = Math.round(d.unitPriceCents * d.quantity)
      const share = mine / total
      return { name: d.name, fraction: `${mine}/${total}`, cents: Math.round(dishValue * share * scale) }
    })
    .filter((l): l is NonNullable<typeof l> => l !== null)

  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-white">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-3 p-4 text-left"
      >
        <span
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-sm font-semibold text-white"
          style={{ background: personColor(person.sortOrder) }}
        >
          {initials(person.name)}
        </span>
        <span className="flex-1 font-medium">{person.name}</span>
        <span className="tabular-nums text-lg font-semibold">{formatCents(owedCents)}</span>
        <ChevronDown size={18} className={open ? 'rotate-180 transition' : 'transition'} />
      </button>
      {open && (
        <div className="border-t border-[var(--color-border)] px-4 py-3 text-sm text-neutral-600">
          {lines.length === 0 ? (
            <p>No items assigned.</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {lines.map((l, i) => (
                <li key={i} className="flex justify-between">
                  <span>
                    {l.fraction} × {l.name}
                  </span>
                  <span className="tabular-nums">{formatCents(l.cents)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
