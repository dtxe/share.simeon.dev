export function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
  'aria-label': ariaLabel = 'Options',
}: {
  value: T
  onChange: (next: T) => void
  options: { value: T; label: string }[]
  'aria-label'?: string
}) {
  return (
    <div role="group" aria-label={ariaLabel} className="flex rounded-lg border border-border bg-surface p-1">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => onChange(opt.value)}
          aria-pressed={value === opt.value}
          className={`flex-1 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
            value === opt.value
              ? 'bg-primary text-primary-foreground'
              : 'text-foreground-muted hover:bg-surface-hover'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}
