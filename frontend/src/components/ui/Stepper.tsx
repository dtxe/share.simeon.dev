import { Minus, Plus } from 'lucide-react'

export function Stepper({
  value,
  onChange,
  min = 0,
}: {
  value: number
  onChange: (next: number) => void
  min?: number
}) {
  return (
    <div className="flex items-center gap-1">
      <button
        type="button"
        aria-label="Decrease"
        disabled={value <= min}
        onClick={() => onChange(value - 1)}
        className="flex h-11 w-11 items-center justify-center rounded-full border border-border bg-surface disabled:opacity-30"
      >
        <Minus size={16} />
      </button>
      <span className="w-6 text-center font-receipt text-sm">{value}</span>
      <button
        type="button"
        aria-label="Increase"
        onClick={() => onChange(value + 1)}
        className="flex h-11 w-11 items-center justify-center rounded-full border border-border bg-surface"
      >
        <Plus size={16} />
      </button>
    </div>
  )
}
