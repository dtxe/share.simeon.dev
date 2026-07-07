import { useRef, useState } from 'react'
import { Sparkles, Trash2 } from 'lucide-react'
import { ApiError, type Dish } from '../lib/api'
import { formatCents } from '../lib/split'
import { toUploadableImage } from '../lib/image'
import { Button } from '../components/ui/Button'

type Stage = 'idle' | 'uploading' | 'parsing' | 'done' | 'failed'

interface ExtractResult {
  restaurantName?: string
  date?: string
  subtotalCents?: number
  tipCents?: number
  totalPaidCents?: number
  items: { name: string; priceCents: number; quantity: number }[]
}

export function ReceiptSection({
  hasReceipt,
  receiptUrl,
  subtotalCents,
  dishes,
  hasPortions,
  onUpload,
  onExtract,
  onAddDish,
  onUpdateDish,
  onDeleteDish,
}: {
  hasReceipt: boolean
  receiptUrl: string | null
  subtotalCents: number
  dishes: Dish[]
  hasPortions: boolean
  onUpload: (file: File | Blob) => Promise<void>
  onExtract: () => Promise<ExtractResult>
  onAddDish: (dish: { name: string; unitPriceCents: number; quantity: number }) => Promise<void>
  onUpdateDish: (dishId: string, patch: Partial<{ name: string; unitPriceCents: number; quantity: number }>) => Promise<void>
  onDeleteDish: (dishId: string) => Promise<void>
}) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [stage, setStage] = useState<Stage>('idle')
  const [error, setError] = useState<string | null>(null)
  const [confirmRescan, setConfirmRescan] = useState(false)

  async function runExtract() {
    setStage('parsing')
    setError(null)
    try {
      const result = await onExtract()
      setStage('done')
      if (result.items.length === 0) {
        setError('No items detected — add them below.')
      }
    } catch (err) {
      setStage('failed')
      if (err instanceof ApiError && err.status === 429) {
        setError('Scan limit reached for this bill — add items below.')
      } else if (err instanceof ApiError && err.status === 503) {
        setError("Scanning isn't available right now — add items below.")
      } else {
        setError("Couldn't read the receipt — add items below.")
      }
    }
  }

  async function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    setStage('uploading')
    setError(null)
    const uploadable = await toUploadableImage(file)
    if (!uploadable) {
      setStage('failed')
      setError('Could not read this photo. Try a JPEG or PNG instead.')
      return
    }
    try {
      await onUpload(uploadable)
    } catch {
      setStage('failed')
      setError('Upload failed. Try again.')
      return
    }
    await runExtract()
  }

  function handleRescanClick() {
    if (hasPortions) {
      setConfirmRescan(true)
      return
    }
    void runExtract()
  }

  return (
    <div className="flex flex-col gap-3">
      <input ref={fileInputRef} type="file" accept="image/*" capture="environment" className="hidden" onChange={handleFile} />

      {!hasReceipt && (
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          disabled={stage === 'uploading' || stage === 'parsing'}
          className="rounded-lg border border-dashed border-[var(--color-border)] bg-white px-4 py-4 text-center text-sm font-medium text-[var(--color-accent)] disabled:opacity-60"
        >
          {stage === 'uploading' ? 'Uploading…' : stage === 'parsing' ? 'Reading your receipt…' : 'Scan receipt — photo or upload'}
        </button>
      )}

      {hasReceipt && (
        <div className="flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-white p-3">
          {receiptUrl && <img src={receiptUrl} alt="Receipt" className="h-16 w-16 rounded object-cover" />}
          <div className="flex flex-1 flex-col gap-1">
            {stage === 'parsing' ? (
              <span className="text-sm text-neutral-500">Reading your receipt…</span>
            ) : confirmRescan ? (
              <div className="flex flex-col gap-2 text-sm">
                <span>Re-scanning replaces all items and clears the split.</span>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    onClick={() => {
                      setConfirmRescan(false)
                      void runExtract()
                    }}
                  >
                    Re-scan
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setConfirmRescan(false)}>
                    Cancel
                  </Button>
                </div>
              </div>
            ) : (
              <button
                type="button"
                onClick={handleRescanClick}
                className="flex items-center gap-1.5 self-start text-sm font-medium text-[var(--color-accent)]"
              >
                <Sparkles size={16} />
                Re-scan receipt
              </button>
            )}
          </div>
        </div>
      )}

      {error && <p className="text-sm text-[var(--color-warn)]">{error}</p>}

      <div className="flex flex-col divide-y divide-[var(--color-border)] rounded-lg border border-[var(--color-border)] bg-white">
        {dishes.map((d) => (
          <DishEditorRow key={d.id} dish={d} onUpdate={(patch) => onUpdateDish(d.id, patch)} onDelete={() => onDeleteDish(d.id)} />
        ))}
        <AddDishRow onAdd={onAddDish} />
      </div>

      <div className="flex items-center justify-between border-t border-dashed border-[var(--color-border)] pt-2 text-sm">
        <span className="text-[var(--color-ink-soft)]">Subtotal</span>
        <span className="font-receipt">{formatCents(subtotalCents)}</span>
      </div>
    </div>
  )
}

function DishEditorRow({
  dish,
  onUpdate,
  onDelete,
}: {
  dish: Dish
  onUpdate: (patch: Partial<{ name: string; unitPriceCents: number; quantity: number }>) => Promise<void>
  onDelete: () => Promise<void>
}) {
  const [name, setName] = useState(dish.name)
  const [quantity, setQuantity] = useState(String(dish.quantity))
  const [priceDollars, setPriceDollars] = useState((dish.unitPriceCents / 100).toFixed(2))

  function commit() {
    const patch: Partial<{ name: string; unitPriceCents: number; quantity: number }> = {}
    const trimmedName = name.trim()
    if (trimmedName && trimmedName !== dish.name) patch.name = trimmedName
    const qty = Number(quantity) || 1
    if (qty !== dish.quantity) patch.quantity = qty
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
        value={quantity}
        onChange={(e) => setQuantity(e.target.value)}
        onBlur={commit}
        type="number"
        min={1}
        className="w-12 rounded border border-[var(--color-border)] px-1 py-1 text-center text-sm font-receipt"
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

function AddDishRow({ onAdd }: { onAdd: (dish: { name: string; unitPriceCents: number; quantity: number }) => Promise<void> }) {
  const [name, setName] = useState('')
  const [quantity, setQuantity] = useState('1')
  const [priceDollars, setPriceDollars] = useState('')
  // Guards against both a premature submit while the user is still tabbing
  // through name/quantity (this only fires from the price field, the last
  // one in the row), and a double-submit race if price's blur fires again
  // before the async add's local-state reset has landed.
  const submittingRef = useRef(false)

  async function commit() {
    const trimmedName = name.trim()
    if (!trimmedName || submittingRef.current) return
    submittingRef.current = true
    const qty = Number(quantity) || 1
    const cents = Math.round(parseFloat(priceDollars || '0') * 100) || 0
    setName('')
    setQuantity('1')
    setPriceDollars('')
    try {
      await onAdd({ name: trimmedName, unitPriceCents: cents, quantity: qty })
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
        value={quantity}
        onChange={(e) => setQuantity(e.target.value)}
        type="number"
        min={1}
        className="w-12 rounded border border-[var(--color-border)] px-1 py-1 text-center text-sm font-receipt"
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
