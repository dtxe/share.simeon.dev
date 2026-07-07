import { useMemo, useState } from 'react'
import type { Dish, Person, Portion } from '../lib/api'
import { previewOwed, unassignedDishIds, formatCents } from '../lib/split'
import { Avatar } from '../components/ui/Avatar'
import { Stepper } from '../components/ui/Stepper'
import { SegmentedControl } from '../components/ui/SegmentedControl'
import { Button } from '../components/ui/Button'

type Mode = 'item' | 'person'

export function AssignSection({
  people,
  dishes,
  portions,
  totalForPreview,
  onAdjust,
  onSplitEvenly,
}: {
  people: Person[]
  dishes: Dish[]
  portions: Portion[]
  totalForPreview: number
  onAdjust: (dishId: string, personId: string, shares: number) => void
  onSplitEvenly: (dishId: string) => void
}) {
  const [mode, setMode] = useState<Mode>('item')
  const [expandedDishId, setExpandedDishId] = useState<string | null>(null)
  const [expandedPersonId, setExpandedPersonId] = useState<string | null>(null)

  const sharesByDish = useMemo(() => {
    const map: Record<string, Record<string, number>> = {}
    for (const p of portions) {
      map[p.dishId] ??= {}
      map[p.dishId][p.personId] = p.shares
    }
    return map
  }, [portions])

  const totalSharesByDish = useMemo(() => {
    const map: Record<string, number> = {}
    for (const p of portions) map[p.dishId] = (map[p.dishId] ?? 0) + p.shares
    return map
  }, [portions])

  const owedByPerson = useMemo(
    () => previewOwed(dishes, portions, people.map((p) => p.id), totalForPreview),
    [dishes, portions, people, totalForPreview],
  )

  const unassigned = new Set(unassignedDishIds(dishes, portions))

  return (
    <div className="flex flex-col gap-3">
      <SegmentedControl
        value={mode}
        onChange={setMode}
        options={[
          { value: 'item', label: unassigned.size > 0 ? `By item (${unassigned.size} left)` : 'By item' },
          { value: 'person', label: 'By person' },
        ]}
      />
      <p className="text-xs text-[var(--color-ink-soft)]">Same split, two views — use whichever is faster.</p>

      {mode === 'item' ? (
        <ul className="flex flex-col divide-y divide-[var(--color-border)] rounded-lg border border-[var(--color-border)] bg-white">
          {dishes.map((d) => {
            const shares = sharesByDish[d.id] ?? {}
            const total = totalSharesByDish[d.id] ?? 0
            const expanded = expandedDishId === d.id
            const assignees = people.filter((p) => (shares[p.id] ?? 0) > 0)
            return (
              <li key={d.id}>
                <button
                  type="button"
                  onClick={() => setExpandedDishId(expanded ? null : d.id)}
                  className="flex w-full items-center justify-between gap-2 p-3 text-left"
                >
                  <span className="flex items-center gap-2">
                    <span>{d.name}</span>
                    {total <= 0 && <span className="rounded-full bg-[var(--color-warn)]/10 px-2 py-0.5 text-xs text-[var(--color-warn)]">unassigned</span>}
                  </span>
                  <span className="flex items-center gap-2">
                    {assignees.length > 0 && (
                      <span className="flex -space-x-2">
                        {assignees.slice(0, 4).map((p) => (
                          <Avatar key={p.id} name={p.name} sortOrder={p.sortOrder} size={24} />
                        ))}
                      </span>
                    )}
                    <span className="font-receipt text-sm">{formatCents(d.unitPriceCents)}</span>
                  </span>
                </button>
                {expanded && (
                  <div className="flex flex-col gap-2 border-t border-dashed border-[var(--color-border)] p-3">
                    {people.map((p) => {
                      const mine = shares[p.id] ?? 0
                      return (
                        <div key={p.id} className="flex items-center justify-between gap-2">
                          <span className="flex items-center gap-2">
                            <Avatar name={p.name} sortOrder={p.sortOrder} size={28} />
                            <span className="text-sm">{p.name}</span>
                            {mine > 0 && total > 0 && (
                              <span className="text-xs text-[var(--color-ink-soft)]">
                                {mine}/{total}
                              </span>
                            )}
                          </span>
                          <Stepper value={mine} onChange={(next) => onAdjust(d.id, p.id, next)} />
                        </div>
                      )
                    })}
                    <Button size="sm" variant="ghost" className="self-start" onClick={() => onSplitEvenly(d.id)}>
                      Split evenly
                    </Button>
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      ) : (
        <ul className="flex flex-col divide-y divide-[var(--color-border)] rounded-lg border border-[var(--color-border)] bg-white">
          {people.map((p) => {
            const expanded = expandedPersonId === p.id
            return (
              <li key={p.id}>
                <button
                  type="button"
                  onClick={() => setExpandedPersonId(expanded ? null : p.id)}
                  className="flex w-full items-center justify-between gap-2 p-3 text-left"
                >
                  <span className="flex items-center gap-2">
                    <Avatar name={p.name} sortOrder={p.sortOrder} size={28} />
                    <span>{p.name}</span>
                  </span>
                  <span className="font-receipt text-sm">{formatCents(owedByPerson[p.id] ?? 0)}</span>
                </button>
                {expanded && (
                  <div className="flex flex-col gap-2 border-t border-dashed border-[var(--color-border)] p-3">
                    {dishes.map((d) => {
                      const mine = sharesByDish[d.id]?.[p.id] ?? 0
                      return (
                        <div key={d.id} className="flex items-center justify-between gap-2">
                          <span className="text-sm">{d.name}</span>
                          <span className="flex items-center gap-2">
                            <span className="font-receipt text-xs text-[var(--color-ink-soft)]">
                              {formatCents(d.unitPriceCents)}
                            </span>
                            <Stepper value={mine} onChange={(next) => onAdjust(d.id, p.id, next)} />
                          </span>
                        </div>
                      )
                    })}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
