import { useRef, useState } from 'react'
import { Loader2, Trash2, Upload } from 'lucide-react'
import { type Dish } from '../lib/api'
import { formatCents } from '../lib/split'
import { Button } from '../components/ui/Button'
import { ReceiptImage } from '../components/ReceiptImage'

export type ReceiptStage = 'idle' | 'uploading' | 'parsing' | 'done' | 'failed'

export function ReceiptSection({
  hasReceipt,
  receiptUrl,
  subtotalCents,
  dishes,
  hasPortions,
  stage,
  error,
  retryable,
  onUpload,
  onAddDish,
  onUpdateDish,
  onDeleteDish,
}: {
  hasReceipt: boolean
  receiptUrl: string | null
  subtotalCents: number
  dishes: Dish[]
  hasPortions: boolean
  stage: ReceiptStage
  error: string | null
  retryable: boolean
  onUpload: (file: File) => Promise<void>
  onAddDish: (dish: { name: string; unitPriceCents: number }) => Promise<void>
  onUpdateDish: (dishId: string, patch: Partial<{ name: string; unitPriceCents: number }>) => Promise<void>
  onDeleteDish: (dishId: string) => Promise<void>
}) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [confirmReupload, setConfirmReupload] = useState(false)

  async function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    setConfirmReupload(false)
    await onUpload(file)
  }

  function handleReuploadClick() {
    if (hasPortions) {
      setConfirmReupload(true)
      return
    }
    fileInputRef.current?.click()
  }

  return (
    <div className="flex flex-col gap-3">
      <input ref={fileInputRef} type="file" accept="image/*" className="hidden" onChange={handleFile} />

      {!hasReceipt && (
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          disabled={stage === 'uploading' || stage === 'parsing'}
          className="flex items-center justify-center gap-2 rounded-lg border border-dashed border-[var(--color-border)] bg-white px-4 py-4 text-center text-sm font-medium text-[var(--color-accent)] disabled:opacity-60"
        >
          {(stage === 'uploading' || stage === 'parsing') && <Loader2 size={16} className="animate-spin" />}
          {stage === 'uploading' ? 'Uploading…' : stage === 'parsing' ? 'Reading your receipt…' : 'Scan receipt — photo or upload'}
        </button>
      )}

      {hasReceipt && (
        <div className="flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-white p-3">
          {receiptUrl && <ReceiptImage src={receiptUrl} size={64} />}
          <div className="flex flex-1 flex-col gap-1">
            {stage === 'uploading' || stage === 'parsing' ? (
              <span className="flex items-center gap-1.5 text-sm text-neutral-500">
                <Loader2 size={14} className="animate-spin" />
                {stage === 'uploading' ? 'Uploading…' : 'Reading your receipt…'}
              </span>
            ) : confirmReupload ? (
              <div className="flex flex-col gap-2 text-sm">
                <span>Re-uploading replaces all items and clears the split.</span>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    onClick={() => {
                      setConfirmReupload(false)
                      fileInputRef.current?.click()
                    }}
                  >
                    Re-upload
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setConfirmReupload(false)}>
                    Cancel
                  </Button>
                </div>
              </div>
            ) : stage === 'failed' && !retryable ? null : (
              <button
                type="button"
                onClick={handleReuploadClick}
                className="flex items-center gap-1.5 self-start text-sm font-medium text-[var(--color-accent)]"
              >
                <Upload size={16} />
                Re-upload photo
              </button>
            )}
          </div>
        </div>
      )}

      {error && <p className="text-sm text-[var(--color-warn)]">{error}</p>}

      <div className="flex flex-col divide-y divide-[var(--color-border)] rounded-lg border border-[var(--color-border)] bg-white">
        {stage === 'parsing' && dishes.length === 0 ? (
          <>
            <ShimmerRow />
            <ShimmerRow />
            <ShimmerRow />
          </>
        ) : (
          <>
            {dishes.map((d) => (
              <DishEditorRow key={d.id} dish={d} onUpdate={(patch) => onUpdateDish(d.id, patch)} onDelete={() => onDeleteDish(d.id)} />
            ))}
            <AddDishRow onAdd={onAddDish} />
          </>
        )}
      </div>

      <div className="flex items-center justify-between border-t border-dashed border-[var(--color-border)] pt-2 text-sm">
        <span className="text-[var(--color-ink-soft)]">Subtotal</span>
        <span className="font-receipt">{formatCents(subtotalCents)}</span>
      </div>
    </div>
  )
}

function ShimmerRow() {
  return (
    <div className="flex items-center gap-2 p-3">
      <div className="h-4 flex-1 animate-pulse rounded bg-[var(--color-border)]" />
      <div className="h-4 w-20 animate-pulse rounded bg-[var(--color-border)]" />
    </div>
  )
}

function DishEditorRow({
  dish,
  onUpdate,
  onDelete,
}: {
  dish: Dish
  onUpdate: (patch: Partial<{ name: string; unitPriceCents: number }>) => Promise<void>
  onDelete: () => Promise<void>
}) {
  const [name, setName] = useState(dish.name)
  const [priceDollars, setPriceDollars] = useState((dish.unitPriceCents / 100).toFixed(2))

  function commit() {
    const patch: Partial<{ name: string; unitPriceCents: number }> = {}
    const trimmedName = name.trim()
    if (trimmedName && trimmedName !== dish.name) patch.name = trimmedName
    const cents = Math.round(parseFloat(priceDollars || '0') * 100) || 0
    if (cents !== dish.unitPriceCents) patch.unitPriceCents = cents
    if (Object.keys(patch).length > 0) void onUpdate(patch)
  }

  return (
    <div className="flex items-center gap-2 p-3">
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        onBlur={commit}
        placeholder="Item name"
        className="min-w-0 flex-1 border-none bg-transparent focus:outline-none"
      />
      <input
        value={priceDollars}
        onChange={(e) => setPriceDollars(e.target.value)}
        onBlur={commit}
        inputMode="decimal"
        placeholder="0.00"
        className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-right font-receipt"
      />
      <button type="button" onClick={() => void onDelete()} aria-label="Remove item" className="text-neutral-400">
        <Trash2 size={16} />
      </button>
    </div>
  )
}

function AddDishRow({ onAdd }: { onAdd: (dish: { name: string; unitPriceCents: number }) => Promise<void> }) {
  const [name, setName] = useState('')
  const [priceDollars, setPriceDollars] = useState('')
  // Guards against both a premature submit while the user is still tabbing
  // through the row (this only fires from the price field, the last one in
  // the row), and a double-submit race if price's blur fires again before the
  // async add's local-state reset has landed.
  const submittingRef = useRef(false)

  async function commit() {
    const trimmedName = name.trim()
    if (!trimmedName || submittingRef.current) return
    submittingRef.current = true
    const cents = Math.round(parseFloat(priceDollars || '0') * 100) || 0
    setName('')
    setPriceDollars('')
    try {
      await onAdd({ name: trimmedName, unitPriceCents: cents })
    } finally {
      submittingRef.current = false
    }
  }

  return (
    <div className="flex items-center gap-2 p-3">
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="+ Add item"
        className="min-w-0 flex-1 border-none bg-transparent text-[var(--color-accent)] focus:outline-none"
      />
      <input
        value={priceDollars}
        onChange={(e) => setPriceDollars(e.target.value)}
        onBlur={() => void commit()}
        inputMode="decimal"
        placeholder="0.00"
        className="w-20 rounded border border-[var(--color-border)] px-2 py-1 text-right font-receipt"
      />
      <span className="w-4" />
    </div>
  )
}
