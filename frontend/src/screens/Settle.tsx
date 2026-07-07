import { useEffect, useRef, useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AppHeader } from '../components/AppHeader'
import { NotAuthorized } from '../components/NotAuthorized'
import { PersonBreakdownCard } from '../components/PersonBreakdownCard'
import { ReceiptImage } from '../components/ReceiptImage'
import { ShareLinkDrawer } from '../components/ShareLinkDrawer'
import { Button } from '../components/ui/Button'
import { api, isAuthError, type Dish, type Person, type Portion } from '../lib/api'
import { formatCents } from '../lib/split'

const EMPTY_PEOPLE: Person[] = []
const EMPTY_DISHES: Dish[] = []
const EMPTY_PORTIONS: Portion[] = []

export default function SettleScreen() {
  const { id } = useParams<{ id: string }>()
  const [, navigate] = useLocation()
  const qc = useQueryClient()

  const { data, error } = useQuery({ queryKey: ['session', id], queryFn: () => api.getSession(id!), enabled: !!id })
  const { data: breakdown } = useQuery({ queryKey: ['breakdown', id], queryFn: () => api.getBreakdown(id!), enabled: !!id })

  const [shareOpen, setShareOpen] = useState(false)
  const [shareUrl, setShareUrl] = useState<string | null>(null)

  const createShare = useMutation({
    mutationFn: () => api.createShare(id!),
    onSuccess: (res) => {
      setShareUrl(res.shareUrl)
      setShareOpen(true)
    },
  })

  const updateTitle = useMutation({
    mutationFn: (title: string) => api.updateSession(id!, { title }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })

  if (isAuthError(error)) return <NotAuthorized />

  const session = data?.session
  const people = data?.people ?? EMPTY_PEOPLE
  const dishes = data?.dishes ?? EMPTY_DISHES
  const portions = data?.portions ?? EMPTY_PORTIONS
  const result = breakdown?.result

  const subtotalCents = session?.subtotalCents ?? 0
  const totalPaidCents = session?.totalPaidCents ?? null
  const taxTip = totalPaidCents != null ? totalPaidCents - subtotalCents : null

  const suggestion = session
    ? [session.restaurantName, session.billDate ? new Date(session.billDate).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : null]
        .filter(Boolean)
        .join(' · ') || `Bill · ${new Date(session.createdAt).toLocaleDateString()}`
    : ''

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col gap-4 px-4 pb-28">
      <AppHeader />

      <button type="button" onClick={() => navigate(`/bill/${id}`)} className="self-start text-sm text-[var(--color-ink-soft)]">
        ← Edit split
      </button>

      {session && <TitleField title={session.title} suggestion={suggestion} onSave={(t) => updateTitle.mutate(t)} />}

      <div className="flex flex-col gap-1 rounded-xl border border-[var(--color-border)] bg-[var(--color-paper)] p-4 font-receipt text-sm">
        <div className="flex justify-between">
          <span className="text-[var(--color-ink-soft)]">Subtotal</span>
          <span>{formatCents(subtotalCents)}</span>
        </div>
        {taxTip != null && (
          <div className="flex justify-between border-t border-dashed border-[var(--color-border)] pt-1">
            <span className="text-[var(--color-ink-soft)]">{taxTip >= 0 ? 'Taxes & tip' : 'Discount'}</span>
            <span>{formatCents(taxTip)}</span>
          </div>
        )}
        <div className="flex justify-between border-t border-dashed border-[var(--color-border)] pt-1 text-base font-semibold">
          <span>Total paid</span>
          <span>{totalPaidCents != null ? formatCents(totalPaidCents) : '—'}</span>
        </div>
      </div>

      {result && result.unassignedDishIds && result.unassignedDishIds.length > 0 && (
        <button
          type="button"
          onClick={() => navigate(`/bill/${id}`)}
          className="rounded-lg border border-[var(--color-warn)] bg-white px-4 py-2 text-left text-sm text-[var(--color-warn)]"
        >
          {result.unassignedDishIds.length} dish(es) still unassigned — tap to fix
        </button>
      )}

      <div className="flex flex-col gap-2">
        {people.map((p, i) => {
          const owed = result?.people.find((r) => r.personId === p.id)?.owedCents ?? 0
          return <PersonBreakdownCard key={p.id} person={p} owedCents={owed} dishes={dishes} portions={portions} defaultOpen={i === 0} />
        })}
      </div>

      {session?.hasReceipt && id && (
        <div className="flex justify-center">
          <ReceiptImage src={api.receiptUrl(id)} size={128} />
        </div>
      )}

      <div className="fixed inset-x-0 bottom-0 mx-auto flex max-w-md items-center justify-between gap-2 border-t border-[var(--color-border)] bg-[var(--color-bg)] p-4">
        <Button variant="secondary" onClick={() => navigate(`/bill/${id}`)}>
          Edit split
        </Button>
        <Button disabled={createShare.isPending} onClick={() => createShare.mutate()}>
          Create share link
        </Button>
      </div>

      <ShareLinkDrawer open={shareOpen} onOpenChange={setShareOpen} shareUrl={shareUrl} />
    </div>
  )
}

function TitleField({
  title,
  suggestion,
  onSave,
}: {
  title: string | null
  suggestion: string
  onSave: (title: string) => void
}) {
  const [value, setValue] = useState(title ?? '')
  const hydrated = useRef(false)

  useEffect(() => {
    if (!hydrated.current) {
      setValue(title ?? '')
      hydrated.current = true
    }
  }, [title])

  function commit() {
    const trimmed = value.trim().slice(0, 120)
    if (trimmed && trimmed !== (title ?? '')) onSave(trimmed)
  }

  return (
    <div className="text-center">
      <input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        placeholder={suggestion}
        className="w-full border-none bg-transparent text-center text-2xl font-semibold focus:outline-none"
      />
      {!value && suggestion && (
        <button
          type="button"
          onClick={() => {
            setValue(suggestion)
            onSave(suggestion)
          }}
          className="mt-1 text-xs text-[var(--color-accent)]"
        >
          Use "{suggestion}"
        </button>
      )}
    </div>
  )
}
