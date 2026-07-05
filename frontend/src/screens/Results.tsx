import { useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery } from '@tanstack/react-query'
import { StepHeader } from '../components/StepHeader'
import { PersonResultCard } from '../components/PersonResultCard'
import { ShareLinkDrawer } from '../components/ShareLinkDrawer'
import { api } from '../lib/api'
import { formatCents } from '../lib/split'

export default function ResultsScreen() {
  const { id } = useParams<{ id: string }>()
  const [, navigate] = useLocation()
  const [shareOpen, setShareOpen] = useState(false)
  const [shareUrl, setShareUrl] = useState<string | null>(null)

  const { data: detail } = useQuery({ queryKey: ['session', id], queryFn: () => api.getSession(id!), enabled: !!id })
  const { data: breakdown } = useQuery({
    queryKey: ['breakdown', id],
    queryFn: () => api.getBreakdown(id!),
    enabled: !!id,
  })

  const share = useMutation({
    mutationFn: () => api.createShare(id!),
    onSuccess: (res) => {
      setShareUrl(res.shareUrl)
      setShareOpen(true)
    },
  })

  if (!detail || !breakdown) {
    return (
      <div className="mx-auto max-w-md p-5">
        <StepHeader sessionId={id!} step="results" />
        <p className="mt-6 text-center text-sm text-neutral-500">Loading…</p>
      </div>
    )
  }

  const { people, dishes, portions, session } = { ...detail, session: breakdown.session }
  const owedByPerson = Object.fromEntries(breakdown.result.people.map((p) => [p.personId, p.owedCents]))

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col gap-4 p-5 pb-28">
      <StepHeader sessionId={id!} step="results" title={session.title ?? undefined} />

      {session.hasReceipt && (
        <img src={api.receiptUrl(id!)} alt="Receipt" className="mx-auto max-h-64 rounded-lg object-contain" />
      )}

      <div className="flex justify-between rounded-lg border border-[var(--color-border)] bg-white p-4 text-sm">
        <span>Subtotal</span>
        <span className="tabular-nums">{formatCents(breakdown.result.subtotalCents)}</span>
      </div>
      <div className="flex justify-between rounded-lg border border-[var(--color-border)] bg-white p-4 text-sm font-medium">
        <span>Total paid</span>
        <span className="tabular-nums">
          {session.totalPaidCents != null ? formatCents(session.totalPaidCents) : '(none entered)'}
        </span>
      </div>

      {breakdown.result.unassignedDishIds && breakdown.result.unassignedDishIds.length > 0 && (
        <p className="text-sm text-[var(--color-warn)]">
          {breakdown.result.unassignedDishIds.length} dish(es) still unassigned — their cost isn't included above.
        </p>
      )}

      <div className="flex flex-col gap-2">
        {people.map((p, i) => (
          <PersonResultCard
            key={p.id}
            person={p}
            owedCents={owedByPerson[p.id] ?? 0}
            dishes={dishes}
            portions={portions}
            subtotalCents={breakdown.result.subtotalCents}
            totalPaidCents={session.totalPaidCents}
            defaultOpen={i === 0}
          />
        ))}
      </div>

      <div className="sticky bottom-4 flex flex-col gap-2">
        <button
          type="button"
          onClick={() => share.mutate()}
          className="w-full rounded-lg bg-[var(--color-accent)] py-3 font-medium text-white"
        >
          Share link
        </button>
        <button
          type="button"
          onClick={() => navigate(`/bill/${id}/assign`)}
          className="w-full rounded-lg border border-[var(--color-border)] py-3 font-medium"
        >
          Edit split
        </button>
      </div>

      <ShareLinkDrawer open={shareOpen} onOpenChange={setShareOpen} shareUrl={shareUrl} />
    </div>
  )
}
