import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import { Avatar } from './ui/Avatar'
import { formatCents } from '../lib/split'

interface BreakdownDish {
  id: string
  name: string
  unitPriceCents: number
}

interface BreakdownPortion {
  dishId: string
  personId: string
  shares: number
}

export function PersonBreakdownCard({
  person,
  owedCents,
  taxCents,
  dishes,
  portions,
  defaultOpen = false,
}: {
  person: { id: string; name: string; sortOrder: number }
  owedCents: number
  taxCents: number
  dishes: BreakdownDish[]
  portions: BreakdownPortion[]
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)

  const totalSharesByDish = new Map<string, number>()
  for (const p of portions) totalSharesByDish.set(p.dishId, (totalSharesByDish.get(p.dishId) ?? 0) + p.shares)

  const lines: { label: string; pct: string; cents: number }[] = []
  let baseSum = 0
  for (const d of dishes) {
    const mine = portions.find((p) => p.dishId === d.id && p.personId === person.id)?.shares ?? 0
    if (mine <= 0) continue
    const total = totalSharesByDish.get(d.id) ?? 0
    if (total <= 0) continue
    const base = Math.round((d.unitPriceCents * mine) / total)
    baseSum += base
    lines.push({ label: d.name, pct: `${formatShare(mine)}/${formatShare(total)} = ${Math.round((mine / total) * 100)}%`, cents: base })
  }
  const adjustment = owedCents - baseSum - taxCents

  return (
    <div className="overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-paper)]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center justify-between gap-3 px-4 py-3.5 text-left"
        aria-expanded={open}
      >
        <span className="flex items-center gap-3">
          <Avatar name={person.name} sortOrder={person.sortOrder} size={36} />
          <span className="font-medium">{person.name}</span>
        </span>
        <span className="flex items-center gap-2">
          <span className="font-receipt text-lg font-semibold">{formatCents(owedCents)}</span>
          <ChevronDown size={18} className={`transition-transform ${open ? 'rotate-180' : ''}`} />
        </span>
      </button>
      {open && (
        <div className="border-t border-dashed border-[var(--color-border)] px-4 py-3 font-receipt text-sm">
          {lines.map((line, i) => (
            <div key={i} className="flex items-center justify-between gap-2 py-0.5">
              <span className="text-[var(--color-ink-soft)]">{line.pct}</span>
              <span className="flex-1 truncate px-2">{line.label}</span>
              <span>{formatCents(line.cents)}</span>
            </div>
          ))}
          {taxCents !== 0 && (
            <div className="flex items-center justify-between gap-2 py-0.5 text-[var(--color-ink-soft)]">
              <span />
              <span className="flex-1 truncate px-2">Tax</span>
              <span>{formatCents(taxCents)}</span>
            </div>
          )}
          {adjustment !== 0 && (
            <div className="flex items-center justify-between gap-2 py-0.5 text-[var(--color-ink-soft)]">
              <span />
              <span className="flex-1 truncate px-2">{adjustment > 0 ? 'Adjustment' : 'Discount'}</span>
              <span>{formatCents(adjustment)}</span>
            </div>
          )}
          <div className="mt-1 flex items-center justify-between gap-2 border-t border-dashed border-[var(--color-border)] pt-1 font-semibold">
            <span />
            <span className="flex-1 px-2">Total</span>
            <span>{formatCents(owedCents)}</span>
          </div>
        </div>
      )}
    </div>
  )
}

function formatShare(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(2)
}
