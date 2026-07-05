import { useParams } from 'wouter'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { formatCents } from '../lib/split'
import { initials, personColor } from '../lib/colors'

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

  return (
    <div className="mx-auto flex max-w-md flex-col gap-4 p-5">
      <header className="pt-4 text-center">
        <h1 className="text-xl font-semibold">{data.title ?? data.restaurantName ?? 'Bill split'}</h1>
        {data.billDate && <p className="text-sm text-neutral-500">{data.billDate}</p>}
      </header>

      {data.hasReceipt && (
        <img src={api.publicReceiptUrl(token!)} alt="Receipt" className="mx-auto max-h-72 rounded-lg object-contain" />
      )}

      <div className="flex justify-between rounded-lg border border-[var(--color-border)] bg-white p-4 text-sm">
        <span>Subtotal</span>
        <span className="tabular-nums">{formatCents(data.subtotalCents)}</span>
      </div>
      <div className="flex justify-between rounded-lg border border-[var(--color-border)] bg-white p-4 text-sm font-medium">
        <span>Total paid</span>
        <span className="tabular-nums">{data.totalPaidCents != null ? formatCents(data.totalPaidCents) : '—'}</span>
      </div>

      <dl className="flex flex-col gap-2">
        {data.result.people.map((p) => {
          const person = data.people.find((person) => person.id === p.personId)
          const name = person?.name ?? 'Guest'
          return (
            <div key={p.personId} className="flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-white p-4">
              <span
                className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-xs font-semibold text-white"
                style={{ background: personColor(person?.sortOrder ?? 0) }}
              >
                {initials(name)}
              </span>
              <dt className="flex-1 font-medium">{name}</dt>
              <dd className="tabular-nums text-lg font-semibold">{formatCents(p.owedCents)}</dd>
            </div>
          )
        })}
      </dl>

      <footer className="py-6 text-center text-xs text-neutral-400">Split with Cher</footer>
    </div>
  )
}
