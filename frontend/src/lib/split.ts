import type { Dish, Portion } from './api'

/**
 * Mirrors backend/internal/split/calculate.go's formula, for instant local
 * preview only while a user is actively adjusting shares. The Results
 * screen always reconciles against GET /sessions/:id/breakdown before
 * showing a number as final — this is never the source of truth.
 */
export function previewOwed(
  dishes: Dish[],
  portions: Portion[],
  peopleIds: string[],
  totalPaidCents: number,
  taxCents: number | null = null,
): Record<string, number> {
  const lineTotal = new Map<string, number>()
  const dishTotalShares = new Map<string, number>()
  const personShares = new Map<string, Map<string, number>>()
  for (const d of dishes) lineTotal.set(d.id, d.unitPriceCents)
  for (const p of portions) {
    if (!Number.isFinite(p.shares) || p.shares <= 0) continue
    dishTotalShares.set(p.dishId, (dishTotalShares.get(p.dishId) ?? 0) + p.shares)
    const shares = personShares.get(p.personId) ?? new Map<string, number>()
    shares.set(p.dishId, (shares.get(p.dishId) ?? 0) + p.shares)
    personShares.set(p.personId, shares)
  }

  let subtotal = 0
  for (const total of lineTotal.values()) subtotal += total

  const raw = new Map<string, number>(peopleIds.map((id) => [id, 0]))
  if (taxCents == null) {
    const scale = subtotal > 0 ? totalPaidCents / subtotal : 0
    for (const id of peopleIds) {
      for (const dish of dishes) {
        const total = dishTotalShares.get(dish.id) ?? 0
        if (total <= 0) continue
        const shares = personShares.get(id)?.get(dish.id) ?? 0
        raw.set(id, (raw.get(id) ?? 0) + (shares / total) * dish.unitPriceCents * scale)
      }
    }
  } else {
    const taxableSubtotal = dishes.reduce((n, d) => n + (d.taxable ? (lineTotal.get(d.id) ?? 0) : 0), 0)
    const residual = totalPaidCents - subtotal - taxCents
    for (const id of peopleIds) {
      for (const dish of dishes) {
        const total = dishTotalShares.get(dish.id) ?? 0
        if (total <= 0) continue
        const shares = personShares.get(id)?.get(dish.id) ?? 0
        const line = lineTotal.get(dish.id) ?? 0
        const tax = dish.taxable && taxableSubtotal > 0 ? (taxCents * line) / taxableSubtotal : 0
        const adjusted = line + tax + (subtotal > 0 ? (residual * line) / subtotal : 0)
        raw.set(id, (raw.get(id) ?? 0) + (shares / total) * adjusted)
      }
    }
  }
  return roundAllocation(raw, peopleIds)
}

function roundAllocation(values: Map<string, number>, ids: string[]): Record<string, number> {
  const out: Record<string, number> = {}
  const remainders = ids.map((id, index) => {
    const value = values.get(id) ?? 0
    const floor = Math.floor(value)
    out[id] = floor
    return { id, index, fraction: value - floor }
  })
  const target = Math.round(ids.reduce((sum, id) => sum + (values.get(id) ?? 0), 0))
  let remaining = target - ids.reduce((sum, id) => sum + out[id], 0)
  remainders.sort((a, b) => b.fraction - a.fraction || a.index - b.index)
  for (let i = 0; remaining > 0 && remainders.length > 0; i += 1) {
    out[remainders[i % remainders.length].id] += 1
    remaining -= 1
  }
  return out
}

export function unassignedDishIds(dishes: Dish[], portions: Portion[]): string[] {
  const totals = new Map<string, number>()
  for (const p of portions) totals.set(p.dishId, (totals.get(p.dishId) ?? 0) + p.shares)
  return dishes.filter((d) => (totals.get(d.id) ?? 0) <= 0).map((d) => d.id)
}

export function formatCents(cents: number): string {
  return (cents / 100).toLocaleString(undefined, {
    style: 'currency',
    currency: 'USD',
    currencyDisplay: 'narrowSymbol',
  })
}
