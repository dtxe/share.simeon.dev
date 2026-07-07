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
): Record<string, number> {
  const lineTotal = new Map<string, number>()
  const dishTotalShares = new Map<string, number>()
  for (const d of dishes) lineTotal.set(d.id, d.unitPriceCents)
  for (const p of portions) dishTotalShares.set(p.dishId, (dishTotalShares.get(p.dishId) ?? 0) + p.shares)

  let subtotal = 0
  for (const total of lineTotal.values()) subtotal += total

  const raw = new Map<string, number>()
  for (const id of peopleIds) raw.set(id, 0)
  for (const p of portions) {
    const total = dishTotalShares.get(p.dishId) ?? 0
    if (total <= 0) continue
    const dishValue = lineTotal.get(p.dishId) ?? 0
    raw.set(p.personId, (raw.get(p.personId) ?? 0) + (p.shares / total) * dishValue)
  }

  const scale = subtotal > 0 ? totalPaidCents / subtotal : 0
  const out: Record<string, number> = {}
  for (const id of peopleIds) out[id] = Math.round((raw.get(id) ?? 0) * scale)
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
