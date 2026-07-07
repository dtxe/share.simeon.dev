import { useState } from 'react'
import { useLocation } from 'wouter'
import { useQuery } from '@tanstack/react-query'
import { Receipt } from 'lucide-react'
import { AppHeader } from '../components/AppHeader'
import { ProfileDrawer } from '../components/ProfileDrawer'
import { Button } from '../components/ui/Button'
import { useMe } from '../auth/useMe'
import { api, type SessionSummary } from '../lib/api'
import { formatCents } from '../lib/split'

export default function HistoryScreen() {
  const [, navigate] = useLocation()
  const { data: me } = useMe()
  const { data: bills } = useQuery({ queryKey: ['myBills'], queryFn: api.getMyBills })
  const [profileOpen, setProfileOpen] = useState(false)

  return (
    <div className="mx-auto flex max-w-md flex-col gap-4 px-4 pb-10">
      <AppHeader />
      <h1 className="text-lg font-semibold">Your bills</h1>

      {me && !me.hasEmail && !me.hasPasskey && (
        <button
          type="button"
          onClick={() => setProfileOpen(true)}
          className="rounded-lg border border-[var(--color-border)] bg-white px-4 py-3 text-left text-sm"
        >
          Add an email or passkey to keep your bills across devices.
        </button>
      )}

      {bills && bills.length === 0 && (
        <div className="flex flex-col items-center gap-3 py-12 text-center">
          <p className="text-sm text-[var(--color-ink-soft)]">No bills yet.</p>
          <Button onClick={() => navigate('/')}>Start a bill</Button>
        </div>
      )}

      {bills && bills.length > 0 && (
        <ul className="flex flex-col gap-2">
          {bills.map((b) => (
            <BillRow key={b.id} bill={b} onOpen={() => navigate(`/bill/${b.id}/settle`)} />
          ))}
        </ul>
      )}

      <ProfileDrawer open={profileOpen} onOpenChange={setProfileOpen} />
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
        className="flex w-full items-center justify-between rounded-lg border border-[var(--color-border)] bg-[var(--color-paper)] px-4 py-3 text-left"
      >
        <span className="flex items-center gap-2">
          {bill.hasReceipt && <Receipt size={16} className="text-[var(--color-ink-soft)]" />}
          <span>
            <span className="block font-medium">{title}</span>
            <span className="block text-xs text-[var(--color-ink-soft)]">{new Date(bill.updatedAt).toLocaleDateString()}</span>
          </span>
        </span>
        <span className="font-receipt text-sm font-medium">{formatCents(bill.subtotalCents)}</span>
      </button>
    </li>
  )
}
