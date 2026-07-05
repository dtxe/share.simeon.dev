import { useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Camera, PencilLine, Users } from 'lucide-react'
import { BigActionCard } from '../components/BigActionCard'
import { SaveHistoryBanner } from '../components/SaveHistoryBanner'
import { useMe } from '../auth/useMe'
import { api, type SessionSummary } from '../lib/api'
import { formatCents } from '../lib/split'

export default function Welcome() {
  const [, navigate] = useLocation()
  const qc = useQueryClient()
  const { data: me } = useMe()
  const { data: bills } = useQuery({ queryKey: ['myBills'], queryFn: api.getMyBills })
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [pendingReceipt, setPendingReceipt] = useState<File | null>(null)

  const createAndGo = useMutation({
    mutationFn: async (next: 'items' | 'people') => {
      const sess = await api.createSession()
      qc.invalidateQueries({ queryKey: ['myBills'] })
      if (pendingReceipt) {
        await api.uploadReceipt(sess.id, pendingReceipt)
      }
      return { sess, next }
    },
    onSuccess: ({ sess, next }) => navigate(`/bill/${sess.id}/${next}`),
  })

  function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setPendingReceipt(file)
    createAndGo.mutate('items')
  }

  return (
    <div className="mx-auto flex max-w-md flex-col gap-6 p-5 pb-24">
      <header className="pt-4 text-center">
        <h1 className="text-2xl font-semibold">Cher</h1>
        <p className="text-sm text-neutral-500">Split a bill with friends, fast.</p>
      </header>

      <div className="flex flex-col gap-3">
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          capture="environment"
          className="hidden"
          onChange={handleFile}
        />
        <BigActionCard
          icon={<Camera size={22} />}
          label="Scan a receipt"
          sublabel="Take a photo or upload one"
          onClick={() => fileInputRef.current?.click()}
        />
        <BigActionCard
          icon={<PencilLine size={22} />}
          label="Type items manually"
          sublabel="Enter dishes and prices yourself"
          onClick={() => createAndGo.mutate('items')}
        />
        <BigActionCard
          icon={<Users size={22} />}
          label="Add people first"
          sublabel="Start with who's splitting the bill"
          onClick={() => createAndGo.mutate('people')}
        />
      </div>

      {me && !me.hasEmail && !me.hasPasskey && <SaveHistoryBanner />}

      {bills && bills.length > 0 && (
        <section>
          <h2 className="mb-2 text-sm font-medium text-neutral-500">Your bills</h2>
          <ul className="flex flex-col gap-2">
            {bills.map((b) => (
              <BillRow key={b.id} bill={b} onOpen={() => navigate(`/bill/${b.id}/results`)} />
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function BillRow({ bill, onOpen }: { bill: SessionSummary; onOpen: () => void }) {
  const title = bill.title ?? bill.restaurantName ?? 'Untitled bill'
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full items-center justify-between rounded-lg border border-[var(--color-border)] bg-white px-4 py-3 text-left"
      >
        <span>
          <span className="block font-medium">{title}</span>
          <span className="block text-xs text-neutral-500">
            {new Date(bill.updatedAt).toLocaleDateString()}
          </span>
        </span>
        <span className="tabular-nums text-sm font-medium">{formatCents(bill.subtotalCents)}</span>
      </button>
    </li>
  )
}
