import { useEffect, useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Sparkles, Trash2 } from 'lucide-react'
import { NotAuthorized } from '../components/NotAuthorized'
import { StepHeader } from '../components/StepHeader'
import { api, isAuthError, type Dish } from '../lib/api'
import { formatCents } from '../lib/split'

interface DraftDish {
  name: string
  priceDollars: string
  quantity: number
}

function toDraft(d: Dish): DraftDish {
  return { name: d.name, priceDollars: (d.unitPriceCents / 100).toFixed(2), quantity: d.quantity }
}

export default function ItemsScreen() {
  const { id } = useParams<{ id: string }>()
  const [, navigate] = useLocation()
  const qc = useQueryClient()

  const { data, error } = useQuery({ queryKey: ['session', id], queryFn: () => api.getSession(id!), enabled: !!id })
  const [draft, setDraft] = useState<DraftDish[]>([])
  const [hydrated, setHydrated] = useState(false)
  const [extracting, setExtracting] = useState(false)
  const [extractError, setExtractError] = useState<string | null>(null)
  const [mismatchWarning, setMismatchWarning] = useState<string | null>(null)

  useEffect(() => {
    if (data && !hydrated) {
      setDraft(data.dishes.map(toDraft))
      setHydrated(true)
    }
  }, [data, hydrated])

  const save = useMutation({
    mutationFn: (dishes: DraftDish[]) =>
      api.replaceDishes(
        id!,
        dishes
          .filter((d) => d.name.trim().length > 0)
          .map((d) => ({
            name: d.name.trim(),
            unitPriceCents: Math.round(parseFloat(d.priceDollars || '0') * 100) || 0,
            quantity: d.quantity || 1,
            source: 'manual',
          })),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })

  if (isAuthError(error)) return <NotAuthorized />

  function commit(next: DraftDish[]) {
    setDraft(next)
    save.mutate(next)
  }

  function updateRow(i: number, patch: Partial<DraftDish>) {
    const next = draft.map((d, idx) => (idx === i ? { ...d, ...patch } : d))
    setDraft(next)
  }

  function addRow() {
    setDraft([...draft, { name: '', priceDollars: '', quantity: 1 }])
  }

  function removeRow(i: number) {
    commit(draft.filter((_, idx) => idx !== i))
  }

  async function runExtraction() {
    setExtracting(true)
    setExtractError(null)
    try {
      const result = await api.extract(id!)
      // Backend already persisted these (extraction endpoint saves server-side
      // now) — just reflect them locally, don't re-save via the manual-entry path.
      const extracted = result.items
        .filter((it) => it.name.trim().length > 0)
        .map((it) => ({
          name: it.name,
          priceDollars: (it.priceCents / 100).toFixed(2),
          quantity: it.quantity,
        }))
      setDraft(extracted)
      const sum = result.items.reduce((s, it) => s + it.priceCents * it.quantity, 0)
      qc.invalidateQueries({ queryKey: ['session', id] })
      setMismatchWarning(sum === 0 ? 'No items detected — try manual entry.' : null)
    } catch {
      setExtractError('Could not read the receipt. Try entering items manually.')
    } finally {
      setExtracting(false)
    }
  }

  const subtotal = draft.reduce((s, d) => s + Math.round(parseFloat(d.priceDollars || '0') * 100) * (d.quantity || 1), 0)
  const canContinue = draft.some((d) => d.name.trim().length > 0)

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col">
      <StepHeader sessionId={id!} step="items" title={data?.session.title ?? undefined} />

      <div className="flex flex-1 flex-col gap-3 p-5 pb-28">
        {data?.session.hasReceipt && (
          <div className="flex items-center justify-between rounded-lg border border-[var(--color-border)] bg-white p-3">
            <img
              src={api.receiptUrl(id!)}
              alt="Receipt"
              className="h-16 w-16 rounded object-cover"
            />
            <button
              type="button"
              disabled={extracting}
              onClick={runExtraction}
              className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
            >
              <Sparkles size={16} />
              {extracting ? 'Reading receipt…' : 'Read items from receipt'}
            </button>
          </div>
        )}
        {extractError && <p className="text-sm text-[var(--color-warn)]">{extractError}</p>}
        {mismatchWarning && <p className="text-sm text-[var(--color-warn)]">{mismatchWarning}</p>}

        <div className="flex flex-col divide-y divide-[var(--color-border)] rounded-lg border border-[var(--color-border)] bg-white">
          {draft.map((d, i) => (
            <div key={i} className="flex items-center gap-2 p-3">
              <input
                value={d.name}
                onChange={(e) => updateRow(i, { name: e.target.value })}
                onBlur={() => commit(draft)}
                placeholder="Item name"
                className="min-w-0 flex-1 border-none bg-transparent focus:outline-none"
              />
              <input
                value={d.quantity}
                onChange={(e) => updateRow(i, { quantity: Number(e.target.value) || 1 })}
                onBlur={() => commit(draft)}
                type="number"
                min={1}
                className="w-12 rounded border border-[var(--color-border)] px-1 py-1 text-center text-sm"
              />
              <input
                value={d.priceDollars}
                onChange={(e) => updateRow(i, { priceDollars: e.target.value })}
                onBlur={() => commit(draft)}
                inputMode="decimal"
                placeholder="0.00"
                className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-right tabular-nums"
              />
              <button type="button" onClick={() => removeRow(i)} aria-label="Remove item" className="text-neutral-400">
                <Trash2 size={16} />
              </button>
            </div>
          ))}
          <button type="button" onClick={addRow} className="p-3 text-left text-sm text-[var(--color-accent)]">
            + Add item
          </button>
        </div>
      </div>

      <div className="sticky bottom-0 flex items-center justify-between border-t border-[var(--color-border)] bg-[var(--color-bg)] p-4">
        <span className="tabular-nums text-sm">Subtotal {formatCents(subtotal)}</span>
        <button
          type="button"
          disabled={!canContinue}
          onClick={() => navigate(`/bill/${id}/assign`)}
          className="rounded-lg bg-[var(--color-accent)] px-6 py-3 font-medium text-white disabled:opacity-40"
        >
          Next
        </button>
      </div>
    </div>
  )
}
