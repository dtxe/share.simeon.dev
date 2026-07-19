import { useEffect, useRef, useState } from 'react'
import { formatCents } from '../lib/split'
import { Button } from '../components/ui/Button'

export function TotalPaidSection({
  subtotalCents,
  taxCents,
  totalPaidCents,
  fromReceipt,
  onSave,
  onSaveTax,
}: {
  subtotalCents: number
  taxCents: number | null
  totalPaidCents: number | null
  fromReceipt: boolean
  onSave: (cents: number) => Promise<void>
  onSaveTax: (cents: number | null) => Promise<void>
}) {
  const [value, setValue] = useState(() => (totalPaidCents != null ? (totalPaidCents / 100).toFixed(2) : ''))
  const [taxValue, setTaxValue] = useState(() => (taxCents != null ? (taxCents / 100).toFixed(2) : ''))
  const userEdited = useRef(false)
  const taxEditing = useRef(false)

  useEffect(() => {
    if (!userEdited.current) {
      setValue(totalPaidCents != null ? (totalPaidCents / 100).toFixed(2) : '')
    }
  }, [totalPaidCents])
  useEffect(() => {
    if (!taxEditing.current) setTaxValue(taxCents != null ? (taxCents / 100).toFixed(2) : '')
  }, [taxCents])

  const parsed = value.trim() === '' ? null : Math.round(parseFloat(value || '0') * 100) || 0
  const delta = parsed != null ? parsed - subtotalCents : 0
  const pct = subtotalCents > 0 ? (delta / subtotalCents) * 100 : 0
  const parsedTax = parseCents(taxValue)
  const suggestedTotal = subtotalCents + (taxCents ?? 0)

  function commit() {
    if (parsed != null) void onSave(parsed)
  }

  function commitTax() {
    taxEditing.current = false
    if (parsedTax != null) {
      void onSaveTax(parsedTax)
      return
    }
    if (taxValue.trim() === '' && taxCents != null) void onSaveTax(null)
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 rounded-lg border border-[var(--color-border)] bg-white px-4 py-3">
        <span className="text-lg">$</span>
        <input
          inputMode="decimal"
          value={value}
          onChange={(e) => {
            userEdited.current = true
            setValue(e.target.value)
          }}
          onBlur={commit}
          placeholder={(suggestedTotal / 100).toFixed(2)}
          className="w-full border-none bg-transparent text-xl font-receipt focus:outline-none"
        />
      </div>
      <label className="flex items-center gap-2 self-start text-xs text-[var(--color-ink-soft)]">
        <span>Tax</span>
        <span className="flex items-center gap-1 rounded border border-[var(--color-border)] bg-white px-2 py-1 font-receipt text-sm text-[var(--color-ink)]">
          <span aria-hidden="true">$</span>
          <input
            aria-label="Tax amount"
            inputMode="decimal"
            value={taxValue}
            onFocus={() => {
              taxEditing.current = true
            }}
            onChange={(e) => setTaxValue(e.target.value)}
            onBlur={commitTax}
            placeholder="—"
            className="w-16 border-none bg-transparent text-right focus:outline-none"
          />
        </span>
      </label>
      {parsed != null && (
        <p className="font-receipt text-sm text-[var(--color-ink-soft)]">
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
            setValue((suggestedTotal / 100).toFixed(2))
            void onSave(suggestedTotal)
          }}
        >
          Use subtotal{taxCents != null ? ' + tax' : ''}
        </Button>
      )}
    </div>
  )
}

function parseCents(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const dollars = Number(trimmed)
  return Number.isFinite(dollars) && dollars >= 0 ? Math.round(dollars * 100) : null
}
