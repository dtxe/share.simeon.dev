import type { Dish, Person } from '../lib/api'
import { PersonChip } from './PersonChip'

interface Props {
  people: Person[]
  selectedDish: Dish | null
  selectedPersonId: string | null
  sharesForSelectedDish: Record<string, number>
  owedByPerson: Record<string, number>
  onSelectPerson: (id: string) => void
  onAdjust: (personId: string, delta: number) => void
  onSplitEvenly: () => void
}

export function PeopleRail({
  people,
  selectedDish,
  selectedPersonId,
  sharesForSelectedDish,
  owedByPerson,
  onSelectPerson,
  onAdjust,
  onSplitEvenly,
}: Props) {
  return (
    <div className="sticky bottom-[64px] border-t border-[var(--color-border)] bg-[var(--color-bg)] px-3 pb-2 pt-2">
      <p aria-live="polite" className="mb-1.5 px-1 text-xs text-neutral-500">
        {selectedDish
          ? `Assigning ${selectedDish.name} — tap people`
          : selectedPersonId
            ? 'Tap dishes to assign — tap again to stop'
            : 'Tap a dish or a person to start assigning'}
      </p>
      <div className="flex items-center gap-3 overflow-x-auto pb-1">
        {selectedDish && (
          <button
            type="button"
            onClick={onSplitEvenly}
            className="shrink-0 rounded-full border border-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-accent)]"
          >
            Split evenly
          </button>
        )}
        {people.map((p) => (
          <PersonChip
            key={p.id}
            name={p.name}
            colorIndex={p.sortOrder}
            selected={p.id === selectedPersonId}
            shareCount={selectedDish ? (sharesForSelectedDish[p.id] ?? 0) : undefined}
            owedCents={!selectedDish ? (owedByPerson[p.id] ?? 0) : undefined}
            onTap={() => (selectedDish ? onAdjust(p.id, 1) : onSelectPerson(p.id))}
            onDecrement={selectedDish ? () => onAdjust(p.id, -1) : undefined}
          />
        ))}
      </div>
    </div>
  )
}
