import { useParams } from 'wouter'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { formatCents } from '../lib/split'
import { PersonBreakdownCard } from '../components/PersonBreakdownCard'
import { ReceiptImage } from '../components/ReceiptImage'

export default function SharedView() {
  const { token } = useParams<{ token: string }>()
  const { data, isLoading, isError } = useQuery({
    queryKey: ['publicView', token],
    queryFn: () => api.getPublicView(token!),
    enabled: !!token,
  })

  if (isLoading) {
    return <p className="p-8 text-center text-sm text-neutral-500">Loading…</p>
  }
  if (isError || !data) {
    return <p className="p-8 text-center text-sm text-neutral-500">This link isn't valid or has expired.</p>
  }

  const dishes = data.dishes ?? []
  const portions = data.portions ?? []
  const canExpand = data.dishes !== undefined

  const subtotalCents = data.subtotalCents
  const totalPaidCents = data.totalPaidCents
  const taxCents = data.taxCents
  const aggregate = totalPaidCents != null ? totalPaidCents - subtotalCents : null
  const adjustment = aggregate != null && taxCents != null ? aggregate - taxCents : null

  return (
    <div className="mx-auto flex max-w-md flex-col gap-4 p-5">
      <header className="pt-4 text-center">
        <h1 className="text-2xl font-semibold">{data.title ?? data.restaurantName ?? 'Bill split'}</h1>
        {data.billDate && <p className="text-sm text-[var(--color-ink-soft)]">{new Date(data.billDate).toLocaleDateString()}</p>}
      </header>

      <div className="flex flex-col gap-1 rounded-xl border border-[var(--color-border)] bg-[var(--color-paper)] p-4 font-receipt text-sm">
        <div className="flex justify-between">
          <span className="text-[var(--color-ink-soft)]">Subtotal</span>
          <span>{formatCents(subtotalCents)}</span>
        </div>
        {taxCents != null && (
          <div className="flex justify-between border-t border-dashed border-[var(--color-border)] pt-1">
            <span className="text-[var(--color-ink-soft)]">Tax</span>
            <span>{formatCents(taxCents)}</span>
          </div>
        )}
        {taxCents == null && aggregate != null && aggregate !== 0 && <ReceiptDelta cents={aggregate} />}
        {adjustment != null && adjustment !== 0 && <ReceiptDelta cents={adjustment} />}
        <div className="flex justify-between border-t border-dashed border-[var(--color-border)] pt-1 text-base font-semibold">
          <span>Total paid</span>
          <span>{totalPaidCents != null ? formatCents(totalPaidCents) : '—'}</span>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        {data.result.people.map((p, i) => {
          const person = data.people.find((person) => person.id === p.personId)
          if (!person) return null
          return canExpand ? (
            <PersonBreakdownCard
              key={p.personId}
              person={person}
              owedCents={p.owedCents}
              taxCents={p.taxCents}
              dishes={dishes}
              portions={portions}
              defaultOpen={i === 0}
            />
          ) : (
            <div key={p.personId} className="flex items-center justify-between rounded-xl border border-[var(--color-border)] bg-[var(--color-paper)] p-4">
              <span className="font-medium">{person.name}</span>
              <span className="font-receipt text-lg font-semibold">{formatCents(p.owedCents)}</span>
            </div>
          )
        })}
      </div>
      {(data.result.unallocatedTaxCents > 0 || data.result.taxDetailsIncomplete) && (
        <p className="text-sm text-[var(--color-warn)]">
          {data.result.unallocatedTaxCents > 0
            ? `${formatCents(data.result.unallocatedTaxCents)} tax is unallocated.`
            : 'Tax details are incomplete.'}
        </p>
      )}

      {data.hasReceipt && (
        <div className="flex justify-center">
          <ReceiptImage src={api.publicReceiptUrl(token!)} size={128} />
        </div>
      )}

      <footer className="py-6 text-center text-xs text-neutral-400">Split with Share</footer>
    </div>
  )
}

function ReceiptDelta({ cents }: { cents: number }) {
  return (
    <div className="flex justify-between">
      <span className="text-[var(--color-ink-soft)]">{cents < 0 ? 'Discount' : 'Adjustment'}</span>
      <span>{formatCents(cents)}</span>
    </div>
  )
}
