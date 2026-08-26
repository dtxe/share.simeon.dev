import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { useParams, useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AppHeader } from '../components/AppHeader'
import { NotAuthorized } from '../components/NotAuthorized'
import { PersonBreakdownCard } from '../components/PersonBreakdownCard'
import { ReceiptImage } from '../components/ReceiptImage'
import { ShareLinkDrawer } from '../components/ShareLinkDrawer'
import { Button } from '../components/ui/Button'
import { api, isAuthError, type Dish, type Person, type Portion } from '../lib/api'
import { formatCents } from '../lib/split'

const EMPTY_PEOPLE: Person[] = []
const EMPTY_DISHES: Dish[] = []
const EMPTY_PORTIONS: Portion[] = []

export default function SettleScreen() {
  const { id } = useParams<{ id: string }>()
  const [, navigate] = useLocation()
  const qc = useQueryClient()

  const { data, error } = useQuery({ queryKey: ['session', id], queryFn: () => api.getSession(id!), enabled: !!id })
  const { data: breakdown } = useQuery({ queryKey: ['breakdown', id], queryFn: () => api.getBreakdown(id!), enabled: !!id })

  const [shareOpen, setShareOpen] = useState(false)
  const [shareUrl, setShareUrl] = useState<string | null>(null)
  const [rotateConfirm, setRotateConfirm] = useState(false)
  const [creatingShareLink, setCreatingShareLink] = useState(false)
  const creatingShareLinkRef = useRef(false)

  useEffect(() => {
    if (data?.session.shareUrl) setShareUrl(data.session.shareUrl)
  }, [data?.session.shareUrl])

  const createShare = useMutation({
    mutationFn: () => api.createShare(id!),
    onSuccess: (res) => {
      setShareUrl(res.shareUrl)
      setShareOpen(true)
    },
  })

  const rotateShare = useMutation({
    mutationFn: () => api.rotateShare(id!),
    onSuccess: (res) => {
      setShareUrl(res.shareUrl)
      setRotateConfirm(false)
      qc.invalidateQueries({ queryKey: ['session', id] })
    },
  })

  const updateTitle = useMutation({
    mutationFn: (title: string) => api.updateSession(id!, { title }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })
  const updateNotes = useMutation({
    mutationFn: (notes: string) => api.updateSession(id!, { notes }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session', id] }),
  })
  const notesField = useRef<NotesFieldHandle>(null)

  async function createShareLink() {
    if (creatingShareLinkRef.current) return
    creatingShareLinkRef.current = true
    setCreatingShareLink(true)
    try {
      await notesField.current?.save()
      await createShare.mutateAsync()
    } catch {
      // Keep the drawer closed when saving notes failed; the field shows the error.
    } finally {
      creatingShareLinkRef.current = false
      setCreatingShareLink(false)
    }
  }

  if (isAuthError(error)) return <NotAuthorized />

  const session = data?.session
  const people = data?.people ?? EMPTY_PEOPLE
  const dishes = data?.dishes ?? EMPTY_DISHES
  const portions = data?.portions ?? EMPTY_PORTIONS
  const result = breakdown?.result
  const shareLinkExists = session?.shareLinkExists || shareUrl !== null
  const shareLinkAvailable = session?.shareLinkAvailable || shareUrl !== null

  const subtotalCents = session?.subtotalCents ?? 0
  const totalPaidCents = session?.totalPaidCents ?? null
  const taxTip = totalPaidCents != null ? totalPaidCents - subtotalCents : null

  const suggestion = session
    ? [session.restaurantName, session.billDate ? new Date(session.billDate).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : null]
        .filter(Boolean)
        .join(' · ') || `Bill · ${new Date(session.createdAt).toLocaleDateString()}`
    : ''

  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col gap-4 px-4 pb-28">
      <AppHeader />

      <button type="button" onClick={() => navigate(`/bill/${id}`)} className="self-start text-sm text-foreground-muted">
        ← Edit split
      </button>

      {session && <TitleField title={session.title} suggestion={suggestion} onSave={(t) => updateTitle.mutate(t)} />}

      <div className="flex flex-col gap-1 rounded-xl border border-border bg-surface p-4 font-receipt text-sm">
        <div className="flex justify-between">
          <span className="text-foreground-muted">Subtotal</span>
          <span>{formatCents(subtotalCents)}</span>
        </div>
        {taxTip != null && (
          <div className="flex justify-between border-t border-dashed border-border pt-1">
            <span className="text-foreground-muted">{taxTip >= 0 ? 'Taxes & tip' : 'Discount'}</span>
            <span>{formatCents(taxTip)}</span>
          </div>
        )}
        <div className="flex justify-between border-t border-dashed border-border pt-1 text-base font-semibold">
          <span>Total paid</span>
          <span>{totalPaidCents != null ? formatCents(totalPaidCents) : '—'}</span>
        </div>
      </div>

      {session && <NotesField ref={notesField} notes={session.notes} onSave={(notes) => updateNotes.mutateAsync(notes)} />}

      {result && result.unassignedDishIds && result.unassignedDishIds.length > 0 && (
        <button
          type="button"
          onClick={() => navigate(`/bill/${id}`)}
          className="rounded-lg border border-warning bg-warning-soft px-4 py-2 text-left text-sm text-warning"
        >
          {result.unassignedDishIds.length} dish(es) still unassigned — tap to fix
        </button>
      )}

      <div className="flex flex-col gap-2">
        {people.map((p, i) => {
          const owed = result?.people.find((r) => r.personId === p.id)?.owedCents ?? 0
          return <PersonBreakdownCard key={p.id} person={p} owedCents={owed} dishes={dishes} portions={portions} defaultOpen={i === 0} />
        })}
      </div>

      {session?.hasReceipt && id && (
        <div className="flex justify-center">
          <ReceiptImage src={api.receiptUrl(id)} size={128} />
        </div>
      )}

      <div className="fixed inset-x-0 bottom-0 mx-auto flex max-w-md items-center justify-between gap-2 border-t border-border bg-background p-4">
        <Button variant="secondary" onClick={() => navigate(`/bill/${id}`)}>
          Edit split
        </Button>
        <Button disabled={creatingShareLink} onClick={() => void createShareLink()}>
          {creatingShareLink ? 'Preparing share link…' : session?.shareLinkExists ? 'Share link' : 'Create share link'}
        </Button>
      </div>

      <ShareLinkDrawer open={shareOpen} onOpenChange={(open) => {
        setShareOpen(open)
        if (!open) setRotateConfirm(false)
      }} shareUrl={shareUrl}
        shareLinkExists={shareLinkExists}
        shareLinkAvailable={shareLinkAvailable}
        rotateConfirm={rotateConfirm} onRotateConfirmChange={setRotateConfirm}
        onRotate={() => rotateShare.mutate()} rotating={rotateShare.isPending} />
    </div>
  )
}

interface NotesFieldHandle {
  save: () => Promise<void>
}

const NOTE_MAX_CODE_POINTS = 500

function limitNoteCodePoints(note: string): string {
  return Array.from(note).slice(0, NOTE_MAX_CODE_POINTS).join('')
}

const NotesField = forwardRef<NotesFieldHandle, { notes: string | null; onSave: (notes: string) => Promise<void> }>(function NotesField(
  { notes, onSave },
  ref,
) {
  const [value, setValue] = useState(notes ?? '')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const valueRef = useRef(value)
  const dirtyRef = useRef(false)
  const serverNotesRef = useRef(notes ?? '')
  const savePromiseRef = useRef<Promise<void> | null>(null)

  useEffect(() => {
    serverNotesRef.current = notes ?? ''
    if (!dirtyRef.current) {
      valueRef.current = notes ?? ''
      setValue(notes ?? '')
    }
  }, [notes])

  const save = useCallback(async (): Promise<void> => {
    if (savePromiseRef.current) {
      await savePromiseRef.current
      return save()
    }

    const next = valueRef.current.trim()
    if (next === serverNotesRef.current) {
      valueRef.current = next
      setValue(next)
      dirtyRef.current = false
      return
    }

    const pending = (async () => {
      setSaving(true)
      setError(null)
      try {
        await onSave(next)
        serverNotesRef.current = next
        if (valueRef.current === next) {
          setValue(next)
          dirtyRef.current = false
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Could not save notes. Try again.')
        throw err
      } finally {
        setSaving(false)
      }
    })()
    savePromiseRef.current = pending
    try {
      await pending
    } finally {
      savePromiseRef.current = null
    }
  }, [onSave])

  useImperativeHandle(ref, () => ({ save }), [save])

  return (
    <div className="rounded-xl border border-border bg-surface p-4">
      <label htmlFor="bill-notes" className="block text-sm font-medium">
        Notes
      </label>
      <textarea
        id="bill-notes"
        value={value}
        onChange={(event) => {
          const next = limitNoteCodePoints(event.target.value)
          valueRef.current = next
          setValue(next)
          dirtyRef.current = true
          setError(null)
        }}
        onBlur={() => void save().catch(() => {})}
        rows={3}
        placeholder="Payment details, context, or anything else to share"
        className="mt-2 w-full resize-y rounded-lg border border-border bg-background px-3 py-2 text-sm leading-6 outline-none placeholder:text-foreground-subtle focus:border-primary"
      />
      <div className="mt-2 flex justify-between gap-3 text-xs text-foreground-muted">
        <span>Visible to anyone with the share link.</span>
        {saving ? <span className="shrink-0">Saving…</span> : <span className="shrink-0">{Array.from(value).length}/{NOTE_MAX_CODE_POINTS}</span>}
      </div>
      {error && <p className="mt-2 text-xs text-danger">{error}</p>}
    </div>
  )
})

function TitleField({
  title,
  suggestion,
  onSave,
}: {
  title: string | null
  suggestion: string
  onSave: (title: string) => void
}) {
  const [value, setValue] = useState(title ?? suggestion)
  const hydrated = useRef(false)

  useEffect(() => {
    if (!hydrated.current) {
      const initialTitle = title ?? suggestion
      setValue(initialTitle)
      hydrated.current = true
      if (!title && initialTitle) onSave(initialTitle)
    }
  }, [title, suggestion, onSave])

  function commit() {
    const trimmed = value.trim().slice(0, 120)
    if (trimmed && trimmed !== (title ?? '')) onSave(trimmed)
  }

  return (
    <div className="text-center">
      <input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        className="w-full border-none bg-transparent text-center text-2xl font-semibold focus:outline-none"
      />
    </div>
  )
}
