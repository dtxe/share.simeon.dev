import { useEffect, useState } from 'react'
import { Drawer } from 'vaul'
import { formatCents } from '../lib/split'

export function TotalPaidDrawer({
  open,
  onOpenChange,
  subtotalCents,
  totalPaidCents,
  onSave,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  subtotalCents: number
  totalPaidCents: number | null
  onSave: (cents: number) => void
}) {
  const [value, setValue] = useState(() => ((totalPaidCents ?? subtotalCents) / 100).toFixed(2))

  // The drawer's underlying component instance persists across open/close
  // (only Drawer.Root's `open` prop toggles), so a useState initializer
  // alone would only ever reflect the very first mount — resync whenever
  // it's reopened, so a subtotal that changed since last time is honored.
  useEffect(() => {
    if (open) setValue(((totalPaidCents ?? subtotalCents) / 100).toFixed(2))
  }, [open, totalPaidCents, subtotalCents])

  const parsed = Math.round(parseFloat(value || '0') * 100) || 0
  const delta = parsed - subtotalCents
  const pct = subtotalCents > 0 ? (delta / subtotalCents) * 100 : 0

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-black/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 rounded-t-2xl bg-white p-6 pb-8">
          <Drawer.Title className="mb-4 text-base font-semibold">Total paid</Drawer.Title>
          <div className="flex items-center gap-2 rounded-lg border border-[var(--color-border)] px-4 py-3">
            <span className="text-lg">$</span>
            <input
              autoFocus
              inputMode="decimal"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              className="w-full border-none bg-transparent text-2xl tabular-nums focus:outline-none"
            />
          </div>
          <p className="mt-2 text-sm text-neutral-500">
            {delta >= 0 ? '+' : ''}
            {formatCents(delta)} ({pct.toFixed(1)}%) vs subtotal {formatCents(subtotalCents)}
          </p>
          <button
            type="button"
            onClick={() => {
              onSave(parsed)
              onOpenChange(false)
            }}
            className="mt-6 w-full rounded-lg bg-[var(--color-accent)] py-3 font-medium text-white"
          >
            Save
          </button>
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
