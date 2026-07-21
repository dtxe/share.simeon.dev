import { useEffect, useRef, useState } from 'react'
import { formatCents } from '../lib/split'
import { Button } from '../components/ui/Button'

export function TotalPaidSection({
  subtotalCents,
  totalPaidCents,
  fromReceipt,
  onSave,
}: {
  subtotalCents: number
  totalPaidCents: number | null
  fromReceipt: boolean
  onSave: (cents: number) => Promise<void>
}) {
  const [value, setValue] = useState(() => (totalPaidCents != null ? (totalPaidCents / 100).toFixed(2) : ''))
  const userEdited = useRef(false)

  useEffect(() => {
    if (!userEdited.current) {
      setValue(totalPaidCents != null ? (totalPaidCents / 100).toFixed(2) : '')
    }
  }, [totalPaidCents])

  const parsed = value.trim() === '' ? null : Math.round(parseFloat(value || '0') * 100) || 0
  const delta = parsed != null ? parsed - subtotalCents : 0
  const pct = subtotalCents > 0 ? (delta / subtotalCents) * 100 : 0

  function commit() {
    if (parsed != null) void onSave(parsed)
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 rounded-lg border border-border bg-surface px-4 py-3">
        <span className="text-lg">$</span>
        <input
          inputMode="decimal"
          value={value}
          onChange={(e) => {
            userEdited.current = true
            setValue(e.target.value)
          }}
          onBlur={commit}
          placeholder={(subtotalCents / 100).toFixed(2)}
          className="w-full border-none bg-transparent text-xl font-receipt focus:outline-none"
        />
      </div>
      {parsed != null && (
        <p className="font-receipt text-sm text-foreground-muted">
          {delta >= 0 ? '+' : ''}
          {formatCents(delta)} ({pct.toFixed(1)}%) vs subtotal {formatCents(subtotalCents)}
          {fromReceipt && !userEdited.current && ' · from your receipt — tap to adjust'}
        </p>
      )}
      {parsed == null && (
        <Button
          size="sm"
          variant="ghost"
          className="self-start"
          onClick={() => {
            userEdited.current = true
            setValue((subtotalCents / 100).toFixed(2))
            void onSave(subtotalCents)
          }}
        >
          Use subtotal
        </Button>
      )}
    </div>
  )
}
