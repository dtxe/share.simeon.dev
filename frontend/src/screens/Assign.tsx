import { useMemo, useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { NotAuthorized } from '../components/NotAuthorized'
import { StepHeader } from '../components/StepHeader'
import { DishRow } from '../components/DishRow'
import { PeopleRail } from '../components/PeopleRail'
import { TotalPaidDrawer } from '../components/TotalPaidDrawer'
import { useAssignSelection } from '../state/assignSelection'
import { api, isAuthError, type Person, type Dish, type Portion } from '../lib/api'
import { previewOwed, unassignedDishIds, formatCents } from '../lib/split'

const EMPTY_PEOPLE: Person[] = []
const EMPTY_DISHES: Dish[] = []
const EMPTY_PORTIONS: Portion[] = []

export default function AssignScreen() {
  const { id } = useParams<{ id: string }>()
  const [, navigate] = useLocation()
  const qc = useQueryClient()
  const [totalPaidOpen, setTotalPaidOpen] = useState(false)
  const [confirmUnassigned, setConfirmUnassigned] = useState(false)

  const { data, error } = useQuery({ queryKey: ['session', id], queryFn: () => api.getSession(id!), enabled: !!id })
  const { selectedDishId, selectedPersonId, selectDish, selectPerson, clearSelection } = useAssignSelection()

  const upsert = useMutation({
    mutationFn: ({ dishId, personId, shares }: { dishId: string; personId: string; shares: number }) =>
      api.upsertPortion(dishId, personId, shares),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })

  const updateTotalPaid = useMutation({
    mutationFn: (cents: number) => api.updateSession(id!, { totalPaidCents: cents }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })

  // Stable empty-array fallbacks (module-level, not recreated per render) so
  // memo dependencies below don't look like they change every render.
  const people = data?.people ?? EMPTY_PEOPLE
  const dishes = data?.dishes ?? EMPTY_DISHES
  const portions = data?.portions ?? EMPTY_PORTIONS

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
    () =>
      previewOwed(
        dishes,
        portions,
        people.map((p) => p.id),
        data?.session.totalPaidCents ?? data?.session.subtotalCents ?? 0,
      ),
    [dishes, portions, people, data],
  )

  const selectedDish = dishes.find((d) => d.id === selectedDishId) ?? null
  const activePerson = people.find((p) => p.id === selectedPersonId) ?? null
  const unassigned = unassignedDishIds(dishes, portions)
  const assignedCount = dishes.length - unassigned.length

  if (isAuthError(error)) return <NotAuthorized />

  function adjust(dishId: string, personId: string, delta: number) {
    const current = sharesByDish[dishId]?.[personId] ?? 0
    const next = Math.max(0, current + delta)
    upsert.mutate({ dishId, personId, shares: next })
  }

  function splitEvenly() {
    if (!selectedDish) return
    for (const p of people) {
      upsert.mutate({ dishId: selectedDish.id, personId: p.id, shares: 1 })
    }
  }

  function goToResults() {
    if (unassigned.length > 0 && !confirmUnassigned) {
      setConfirmUnassigned(true)
      return
    }
    navigate(`/bill/${id}/results`)
  }

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col">
      <StepHeader sessionId={id!} step="assign" title={data?.session.title ?? undefined} />

      <div className="flex-1 overflow-y-auto pb-2">
        <p className="px-4 py-2 text-xs text-neutral-500">
          {assignedCount} of {dishes.length} dishes assigned
        </p>
        <div className="mx-4 rounded-lg border border-[var(--color-border)] bg-white">
          {dishes.map((d) => (
            <DishRow
              key={d.id}
              dish={d}
              people={people}
              sharesByPerson={sharesByDish[d.id] ?? {}}
              totalShares={totalSharesByDish[d.id] ?? 0}
              selected={d.id === selectedDishId}
              activePersonForStepper={selectedPersonId ? activePerson : null}
              onSelect={() => selectDish(d.id)}
              onAdjust={(personId, delta) => adjust(d.id, personId, delta)}
            />
          ))}
        </div>
      </div>

      {confirmUnassigned && (
        <div className="mx-4 mb-2 rounded-lg border border-[var(--color-warn)] bg-white p-3 text-sm">
          <p className="mb-2">{unassigned.length} dish(es) unassigned.</p>
          <div className="flex gap-2">
            <button
              type="button"
              className="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-white"
              onClick={() => {
                for (const dishId of unassigned) {
                  for (const p of people) upsert.mutate({ dishId, personId: p.id, shares: 1 })
                }
                setConfirmUnassigned(false)
              }}
            >
              Split them evenly
            </button>
            <button
              type="button"
              className="rounded-md border border-[var(--color-border)] px-3 py-1.5"
              onClick={() => setConfirmUnassigned(false)}
            >
              Go back
            </button>
          </div>
        </div>
      )}

      <PeopleRail
        people={people}
        selectedDish={selectedDish}
        selectedPersonId={selectedPersonId}
        sharesForSelectedDish={selectedDish ? (sharesByDish[selectedDish.id] ?? {}) : {}}
        owedByPerson={owedByPerson}
        onSelectPerson={selectPerson}
        onAdjust={(personId, delta) => selectedDish && adjust(selectedDish.id, personId, delta)}
        onSplitEvenly={splitEvenly}
      />

      <div className="sticky bottom-0 flex items-center justify-between border-t border-[var(--color-border)] bg-[var(--color-bg)] p-4">
        <button
          type="button"
          onClick={() => {
            clearSelection()
            setTotalPaidOpen(true)
          }}
          className="text-sm text-neutral-600"
        >
          Total paid: {data?.session.totalPaidCents != null ? formatCents(data.session.totalPaidCents) : '—'}
        </button>
        <button
          type="button"
          onClick={goToResults}
          className="rounded-lg bg-[var(--color-accent)] px-6 py-3 font-medium text-white"
        >
          Review split →
        </button>
      </div>

      {data && (
        <TotalPaidDrawer
          open={totalPaidOpen}
          onOpenChange={setTotalPaidOpen}
          subtotalCents={data.session.subtotalCents}
          totalPaidCents={data.session.totalPaidCents}
          onSave={(cents) => updateTotalPaid.mutate(cents)}
        />
      )}
    </div>
  )
}
