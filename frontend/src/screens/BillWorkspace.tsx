import { useEffect, useRef, useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AppHeader } from '../components/AppHeader'
import { NotAuthorized } from '../components/NotAuthorized'
import { Section } from '../components/ui/Section'
import { Button } from '../components/ui/Button'
import { ReceiptSection } from '../sections/ReceiptSection'
import { PeopleSection } from '../sections/PeopleSection'
import { TotalPaidSection } from '../sections/TotalPaidSection'
import { AssignSection } from '../sections/AssignSection'
import { useEnsureSession } from '../hooks/useEnsureSession'
import { api, isAuthError, type Person, type Dish, type Portion, type SessionDetail } from '../lib/api'
import { unassignedDishIds, formatCents } from '../lib/split'

const EMPTY_PEOPLE: Person[] = []
const EMPTY_DISHES: Dish[] = []
const EMPTY_PORTIONS: Portion[] = []

type SectionId = 'receipt' | 'people' | 'total' | 'assign'
const SECTION_ORDER: SectionId[] = ['receipt', 'people', 'total', 'assign']

export default function BillWorkspace() {
  const { id: routeId } = useParams<{ id?: string }>()
  const [, navigate] = useLocation()
  const qc = useQueryClient()
  const ensure = useEnsureSession(routeId)

  const { data, error } = useQuery({
    queryKey: ['session', routeId],
    queryFn: () => api.getSession(routeId!),
    enabled: !!routeId,
  })

  const session = data?.session
  const people = data?.people ?? EMPTY_PEOPLE
  const dishes = data?.dishes ?? EMPTY_DISHES
  const portions = data?.portions ?? EMPTY_PORTIONS

  function invalidate(id: string) {
    qc.invalidateQueries({ queryKey: ['session', id] })
  }

  // --- section open/collapse orchestration ---
  const [open, setOpen] = useState<Record<SectionId, boolean>>({
    receipt: true,
    people: false,
    total: false,
    assign: false,
  })
  const [userToggled, setUserToggled] = useState<Record<SectionId, boolean>>({
    receipt: false,
    people: false,
    total: false,
    assign: false,
  })

  const unassigned = unassignedDishIds(dishes, portions)
  const complete: Record<SectionId, boolean> = {
    receipt: dishes.length > 0,
    people: people.length > 0,
    total: session?.totalPaidCents != null,
    assign: dishes.length > 0 && unassigned.length === 0,
  }

  const initialized = useRef(false)
  useEffect(() => {
    if (initialized.current) return
    if (routeId && !data) return
    initialized.current = true
    const firstIncomplete = SECTION_ORDER.find((id) => !complete[id]) ?? null
    setOpen((prev) => {
      const next = { ...prev }
      for (const id of SECTION_ORDER) next[id] = id === firstIncomplete
      return next
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, routeId])

  const prevComplete = useRef(complete)
  useEffect(() => {
    if (!initialized.current) {
      prevComplete.current = complete
      return
    }
    let changed = false
    const next = { ...open }
    for (const id of SECTION_ORDER) {
      if (!prevComplete.current[id] && complete[id] && !userToggled[id]) {
        next[id] = false
        const idx = SECTION_ORDER.indexOf(id)
        for (let i = idx + 1; i < SECTION_ORDER.length; i++) {
          if (!complete[SECTION_ORDER[i]]) {
            next[SECTION_ORDER[i]] = true
            break
          }
        }
        changed = true
      }
    }
    prevComplete.current = complete
    if (changed) setOpen(next)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [complete.receipt, complete.people, complete.total, complete.assign])

  function toggleSection(id: SectionId) {
    setUserToggled((prev) => ({ ...prev, [id]: true }))
    setOpen((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  // --- mutations ---
  const uploadReceipt = async (file: File | Blob) => {
    const id = await ensure()
    await api.uploadReceipt(id, file)
    invalidate(id)
  }

  const runExtract = async () => {
    // Use ensure() (not routeId directly) — this can be invoked by a stale
    // closure captured before the very first upload's session-creating
    // navigation lands, and ensure()'s inflight-promise cache still resolves
    // to the right id regardless of which render's closure calls it.
    const id = await ensure()
    const result = await api.extract(id)
    invalidate(id)
    return result
  }

  const addDish = async (dish: { name: string; unitPriceCents: number; quantity: number }) => {
    const id = await ensure()
    await api.addDish(id, dish)
    invalidate(id)
  }
  const updateDish = async (dishId: string, patch: Partial<{ name: string; unitPriceCents: number; quantity: number }>) => {
    await api.updateDish(dishId, patch)
    if (routeId) invalidate(routeId)
  }
  const deleteDish = async (dishId: string) => {
    await api.deleteDish(dishId)
    if (routeId) invalidate(routeId)
  }

  const addPeopleMany = async (names: string[]): Promise<string[]> => {
    const id = await ensure()
    const failed: string[] = []
    for (const name of names) {
      try {
        await api.addPerson(id, name)
      } catch {
        failed.push(name)
      }
    }
    invalidate(id)
    return failed
  }
  const renamePerson = async (personId: string, name: string) => {
    await api.renamePerson(personId, name)
    if (routeId) invalidate(routeId)
  }
  const deletePerson = async (personId: string) => {
    await api.deletePerson(personId)
    if (routeId) invalidate(routeId)
  }

  const saveTotalPaid = async (cents: number) => {
    const id = await ensure()
    await api.updateSession(id, { totalPaidCents: cents })
    invalidate(id)
  }

  const upsertPortion = useMutation({
    mutationFn: async (vars: { dishId: string; personId: string; shares: number }) => {
      const id = await ensure()
      await api.upsertPortion(vars.dishId, vars.personId, vars.shares)
      return id
    },
    onMutate: async (vars) => {
      if (!routeId) return undefined
      await qc.cancelQueries({ queryKey: ['session', routeId] })
      const prev = qc.getQueryData<SessionDetail>(['session', routeId])
      if (prev) {
        const idx = prev.portions.findIndex((p) => p.dishId === vars.dishId && p.personId === vars.personId)
        const nextPortions =
          idx >= 0
            ? prev.portions.map((p, i) => (i === idx ? { ...p, shares: vars.shares } : p))
            : [...prev.portions, { dishId: vars.dishId, personId: vars.personId, shares: vars.shares }]
        qc.setQueryData(['session', routeId], { ...prev, portions: nextPortions })
      }
      return { prev }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev && routeId) qc.setQueryData(['session', routeId], ctx.prev)
    },
    onSettled: () => {
      if (routeId) qc.invalidateQueries({ queryKey: ['session', routeId] })
    },
  })

  function adjust(dishId: string, personId: string, shares: number) {
    upsertPortion.mutate({ dishId, personId, shares: Math.max(0, shares) })
  }
  function splitEvenly(dishId: string) {
    for (const p of people) upsertPortion.mutate({ dishId, personId: p.id, shares: 1 })
  }

  const [confirmUnassigned, setConfirmUnassigned] = useState(false)
  function goToSettle() {
    if (unassigned.length > 0 && !confirmUnassigned) {
      setConfirmUnassigned(true)
      return
    }
    navigate(`/bill/${routeId}/settle`)
  }

  if (isAuthError(error)) return <NotAuthorized />

  const assignedCount = dishes.length - unassigned.length

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col gap-4 px-4 pb-28">
      <AppHeader />

      <p className="text-sm font-medium text-[var(--color-ink-soft)]">The bill</p>

      <Section
        title="Receipt & items"
        open={open.receipt}
        onToggle={() => toggleSection('receipt')}
        complete={complete.receipt}
        summary={dishes.length > 0 ? `${dishes.length} items, ${formatCents(session?.subtotalCents ?? 0)} subtotal` : undefined}
      >
        <ReceiptSection
          hasReceipt={session?.hasReceipt ?? false}
          receiptUrl={routeId && session?.hasReceipt ? api.receiptUrl(routeId) : null}
          subtotalCents={session?.subtotalCents ?? 0}
          dishes={dishes}
          hasPortions={portions.some((p) => p.shares > 0)}
          onUpload={uploadReceipt}
          onExtract={runExtract}
          onAddDish={addDish}
          onUpdateDish={updateDish}
          onDeleteDish={deleteDish}
        />
      </Section>

      <Section
        title="Who's in"
        open={open.people}
        onToggle={() => toggleSection('people')}
        complete={complete.people}
        summary={people.length > 0 ? `${people.length} people` : undefined}
      >
        <PeopleSection people={people} portions={portions} onAddMany={addPeopleMany} onRename={renamePerson} onDelete={deletePerson} />
      </Section>

      <Section
        title="Total paid"
        open={open.total}
        onToggle={() => toggleSection('total')}
        complete={complete.total}
        summary={
          session?.totalPaidCents != null
            ? `${formatCents(session.totalPaidCents)}${
                session.subtotalCents > 0
                  ? ` (${session.totalPaidCents - session.subtotalCents >= 0 ? '+' : ''}${(((session.totalPaidCents - session.subtotalCents) / session.subtotalCents) * 100).toFixed(1)}%)`
                  : ''
              } incl. tax and tip`
            : undefined
        }
      >
        <TotalPaidSection
          subtotalCents={session?.subtotalCents ?? 0}
          totalPaidCents={session?.totalPaidCents ?? null}
          fromReceipt={dishes.some((d) => d.source === 'llm_extracted')}
          onSave={saveTotalPaid}
        />
      </Section>

      <Section
        title="Divvy up"
        open={open.assign}
        onToggle={() => toggleSection('assign')}
        complete={complete.assign}
        summary={complete.assign ? 'All matched' : undefined}
        warn={!complete.assign && dishes.length > 0 ? <span className="text-[var(--color-warn)]">{unassigned.length} unmatched</span> : undefined}
      >
        <AssignSection
          people={people}
          dishes={dishes}
          portions={portions}
          totalForPreview={session?.totalPaidCents ?? session?.subtotalCents ?? 0}
          onAdjust={adjust}
          onSplitEvenly={splitEvenly}
        />
      </Section>

      {confirmUnassigned && (
        <div className="fixed inset-x-0 bottom-16 z-10 mx-auto max-w-md rounded-lg border border-[var(--color-warn)] bg-white p-3 text-sm shadow-lg">
          <p className="mb-2">{unassigned.length} dish(es) unassigned.</p>
          <div className="flex gap-2">
            <Button
              size="sm"
              onClick={() => {
                for (const dishId of unassigned) splitEvenly(dishId)
                setConfirmUnassigned(false)
              }}
            >
              Split them evenly
            </Button>
            <Button size="sm" variant="secondary" onClick={() => setConfirmUnassigned(false)}>
              Keep editing
            </Button>
          </div>
        </div>
      )}

      <div className="fixed inset-x-0 bottom-0 mx-auto flex max-w-md items-center justify-between border-t border-[var(--color-border)] bg-[var(--color-bg)] p-4">
        <span className="font-receipt text-sm text-[var(--color-ink-soft)]">
          {dishes.length > 0 ? `${assignedCount} of ${dishes.length} assigned` : formatCents(session?.subtotalCents ?? 0)}
        </span>
        <Button disabled={dishes.length === 0 || people.length === 0} onClick={goToSettle}>
          Settle up →
        </Button>
      </div>
    </div>
  )
}
