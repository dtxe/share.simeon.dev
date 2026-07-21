import { useState } from 'react'
import { Trash2 } from 'lucide-react'
import type { Person, Portion } from '../lib/api'
import { Avatar } from '../components/ui/Avatar'
import { Button } from '../components/ui/Button'

export function PeopleSection({
  people,
  portions,
  onAddMany,
  onRename,
  onDelete,
}: {
  people: Person[]
  portions: Portion[]
  onAddMany: (names: string[]) => Promise<string[]>
  onRename: (personId: string, name: string) => Promise<void>
  onDelete: (personId: string) => Promise<void>
}) {
  const [bulkOpen, setBulkOpen] = useState(people.length === 0)
  const [bulkText, setBulkText] = useState('')
  const [bulkError, setBulkError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)

  async function submitBulk() {
    const seen = new Set<string>()
    const names = bulkText
      .split('\n')
      .map((n) => n.trim())
      .filter((n) => n.length > 0 && n.length <= 50)
      .filter((n) => {
        const key = n.toLowerCase()
        if (seen.has(key)) return false
        seen.add(key)
        return true
      })
    if (names.length === 0) return
    setPending(true)
    setBulkError(null)
    const failed = await onAddMany(names)
    setPending(false)
    if (failed.length > 0) {
      setBulkText(failed.join('\n'))
      setBulkError(`Couldn't add: ${failed.join(', ')}`)
    } else {
      setBulkText('')
      setBulkOpen(false)
    }
  }

  function personHasPortions(personId: string) {
    return portions.some((p) => p.personId === personId && p.shares > 0)
  }

  return (
    <div className="flex flex-col gap-3">
      {people.length > 0 && (
        <ul className="flex flex-col divide-y divide-border rounded-lg border border-border bg-surface">
          {people.map((p) => (
            <li key={p.id} className="flex items-center gap-3 p-3">
              <Avatar name={p.name} sortOrder={p.sortOrder} size={36} />
              <input
                defaultValue={p.name}
                onBlur={(e) => {
                  const next = e.target.value.trim()
                  if (next && next !== p.name) void onRename(p.id, next)
                  else e.target.value = p.name
                }}
                className="min-w-0 flex-1 border-none bg-transparent focus:outline-none"
              />
              {confirmDelete === p.id ? (
                <div className="flex items-center gap-2 text-xs">
                  <span className="text-danger">Remove?</span>
                  <button type="button" className="font-medium text-danger" onClick={() => void onDelete(p.id).then(() => setConfirmDelete(null))}>
                    Yes
                  </button>
                  <button type="button" className="text-foreground-subtle" onClick={() => setConfirmDelete(null)}>
                    No
                  </button>
                </div>
              ) : (
                <button
                  type="button"
                  aria-label={`Remove ${p.name}`}
                  className="text-foreground-subtle"
                  onClick={() => {
                    if (personHasPortions(p.id)) setConfirmDelete(p.id)
                    else void onDelete(p.id)
                  }}
                >
                  <Trash2 size={16} />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {bulkOpen ? (
        <div className="flex flex-col gap-2">
          <textarea
            value={bulkText}
            onChange={(e) => setBulkText(e.target.value)}
            placeholder="One name per line"
            rows={4}
            className="rounded-lg border border-border bg-surface px-3 py-2"
          />
          {bulkError && <p className="text-sm text-danger">{bulkError}</p>}
          <div className="flex gap-2">
            <Button size="sm" disabled={pending} onClick={() => void submitBulk()}>
              {pending ? 'Adding…' : 'Add everyone'}
            </Button>
            {people.length > 0 && (
              <Button size="sm" variant="secondary" onClick={() => setBulkOpen(false)}>
                Cancel
              </Button>
            )}
          </div>
        </div>
      ) : (
        <button type="button" onClick={() => setBulkOpen(true)} className="self-start text-sm font-medium text-primary">
          + Add people
        </button>
      )}
    </div>
  )
}
