export function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
}: {
  value: T
  onChange: (next: T) => void
  options: { value: T; label: string }[]
}) {
  return (
    <div className="flex rounded-lg border border-[var(--color-border)] bg-white p-1">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => onChange(opt.value)}
          className={`flex-1 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
            value === opt.value
              ? 'bg-[var(--color-accent)] text-white'
              : 'text-[var(--color-ink-soft)] hover:bg-neutral-50'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}
