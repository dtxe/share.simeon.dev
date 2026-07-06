import { useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Camera, PencilLine, Users } from 'lucide-react'
import { BigActionCard } from '../components/BigActionCard'
import { SaveHistoryBanner } from '../components/SaveHistoryBanner'
import { useMe } from '../auth/useMe'
import { api, type SessionSummary } from '../lib/api'
import { formatCents } from '../lib/split'

// Safari/iOS decode HEIC natively at the OS level, which handles profiles
// (HDR, Live Photo main image, 10-bit) that the heic2any WASM fallback
// below doesn't. Try that path first via canvas; it's a silent no-op
// (returns null) on browsers with no native HEIC support.
async function heicToJpegViaCanvas(file: File): Promise<Blob | null> {
  try {
    const bitmap = await createImageBitmap(file)
    const canvas = document.createElement('canvas')
    canvas.width = bitmap.width
    canvas.height = bitmap.height
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.drawImage(bitmap, 0, 0)
    return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.9))
  } catch {
    return null
  }
}

// iOS camera capture defaults to HEIC, which the backend can't decode.
// `file.type` is often empty for camera-captured HEIC in Safari, so also
// check the extension.
// Returns null if the file can't be converted — the server can't decode
// HEIC either, so there's no point uploading it and getting a 400 back.
async function toUploadableImage(file: File): Promise<File | Blob | null> {
  const isHeic = file.type === 'image/heic' || file.type === 'image/heif' || /\.hei[cf]$/i.test(file.name)
  if (!isHeic) return file

  const viaCanvas = await heicToJpegViaCanvas(file)
  if (viaCanvas) return viaCanvas

  try {
    const heic2any = (await import('heic2any')).default
    const converted = await heic2any({ blob: file, toType: 'image/jpeg', quality: 0.9 })
    return Array.isArray(converted) ? converted[0] : converted
  } catch (err) {
    console.error('HEIC conversion failed', err)
    return null
  }
}

export default function Welcome() {
  const [, navigate] = useLocation()
  const qc = useQueryClient()
  const { data: me } = useMe()
  const { data: bills } = useQuery({ queryKey: ['myBills'], queryFn: api.getMyBills })
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [pendingReceipt, setPendingReceipt] = useState<File | Blob | null>(null)
  const [fileError, setFileError] = useState<string | null>(null)

  const createAndGo = useMutation({
    mutationFn: async (next: 'items' | 'people') => {
      const sess = await api.createSession()
      qc.invalidateQueries({ queryKey: ['myBills'] })
      if (pendingReceipt) {
        await api.uploadReceipt(sess.id, pendingReceipt)
      }
      return { sess, next }
    },
    onSuccess: ({ sess, next }) => navigate(`/bill/${sess.id}/${next}`),
  })

  async function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    const uploadable = await toUploadableImage(file)
    if (!uploadable) {
      setFileError('Could not read this photo. Try a JPEG or PNG instead.')
      e.target.value = ''
      return
    }
    setFileError(null)
    setPendingReceipt(uploadable)
    createAndGo.mutate('items')
  }

  return (
    <div className="mx-auto flex max-w-md flex-col gap-6 p-5 pb-24">
      <header className="pt-4 text-center">
        <h1 className="text-2xl font-semibold">Share</h1>
        <p className="text-sm text-neutral-500">Split a bill with friends, fast.</p>
      </header>

      <div className="flex flex-col gap-3">
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          capture="environment"
          className="hidden"
          onChange={handleFile}
        />
        <BigActionCard
          icon={<Camera size={22} />}
          label="Scan a receipt"
          sublabel="Take a photo or upload one"
          onClick={() => fileInputRef.current?.click()}
        />
        {fileError && <p className="text-sm text-red-600">{fileError}</p>}
        <BigActionCard
          icon={<PencilLine size={22} />}
          label="Type items manually"
          sublabel="Enter dishes and prices yourself"
          onClick={() => createAndGo.mutate('items')}
        />
        <BigActionCard
          icon={<Users size={22} />}
          label="Add people first"
          sublabel="Start with who's splitting the bill"
          onClick={() => createAndGo.mutate('people')}
        />
      </div>

      {me && !me.hasPasskey && <SaveHistoryBanner />}

      {bills && bills.length > 0 && (
        <section>
          <h2 className="mb-2 text-sm font-medium text-neutral-500">Your bills</h2>
          <ul className="flex flex-col gap-2">
            {bills.map((b) => (
              <BillRow key={b.id} bill={b} onOpen={() => navigate(`/bill/${b.id}/results`)} />
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function BillRow({ bill, onOpen }: { bill: SessionSummary; onOpen: () => void }) {
  const title = bill.title ?? bill.restaurantName ?? 'Untitled bill'
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full items-center justify-between rounded-lg border border-[var(--color-border)] bg-white px-4 py-3 text-left"
      >
        <span>
          <span className="block font-medium">{title}</span>
          <span className="block text-xs text-neutral-500">
            {new Date(bill.updatedAt).toLocaleDateString()}
          </span>
        </span>
        <span className="tabular-nums text-sm font-medium">{formatCents(bill.subtotalCents)}</span>
      </button>
    </li>
  )
}
