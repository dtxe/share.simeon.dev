import { useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { StepHeader } from '../components/StepHeader'
import { api } from '../lib/api'
import { personColor, initials } from '../lib/colors'

export default function PeopleScreen() {
  const { id } = useParams<{ id: string }>()
  const [, navigate] = useLocation()
  const qc = useQueryClient()
  const [name, setName] = useState('')

  const { data } = useQuery({ queryKey: ['session', id], queryFn: () => api.getSession(id!), enabled: !!id })

  const addPerson = useMutation({
    mutationFn: (n: string) => api.addPerson(id!, n),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['session', id] })
      setName('')
    },
  })

  const deletePerson = useMutation({
    mutationFn: (personId: string) => api.deletePerson(personId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = name.trim()
    if (trimmed.length < 1 || trimmed.length > 50) return
    addPerson.mutate(trimmed)
  }

  const people = data?.people ?? []
  const canContinue = people.length >= 2

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col">
      <StepHeader sessionId={id!} step="people" title={data?.session.title ?? undefined} />

      <div className="flex flex-1 flex-col gap-4 p-5">
        <form onSubmit={submit} className="flex gap-2">
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Add a name"
            enterKeyHint="done"
            className="flex-1 rounded-lg border border-[var(--color-border)] bg-white px-4 py-3"
          />
          <button
            type="submit"
            className="rounded-lg bg-[var(--color-accent)] px-4 py-3 font-medium text-white"
          >
            +
          </button>
        </form>

        <div className="flex flex-wrap gap-2">
          {people.map((p) => (
            <span
              key={p.id}
              className="flex items-center gap-2 rounded-full border border-[var(--color-border)] bg-white py-1.5 pl-1.5 pr-3"
            >
              <span
                className="flex h-7 w-7 items-center justify-center rounded-full text-xs font-semibold text-white"
                style={{ background: personColor(p.sortOrder) }}
              >
                {initials(p.name)}
              </span>
              {p.name}
              <button
                type="button"
                aria-label={`Remove ${p.name}`}
                onClick={() => deletePerson.mutate(p.id)}
                className="text-neutral-400"
              >
                <X size={14} />
              </button>
            </span>
          ))}
        </div>
      </div>

      <div className="sticky bottom-0 border-t border-[var(--color-border)] bg-[var(--color-bg)] p-4">
        <button
          type="button"
          disabled={!canContinue}
          onClick={() => navigate(`/bill/${id}/items`)}
          className="w-full rounded-lg bg-[var(--color-accent)] py-3 font-medium text-white disabled:opacity-40"
        >
          Next
        </button>
      </div>
    </div>
  )
}
