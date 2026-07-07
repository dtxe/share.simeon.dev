import { useParams } from 'wouter'

export default function SettleScreen() {
  const { id } = useParams<{ id: string }>()
  return (
    <div className="mx-auto max-w-md p-5 text-sm text-neutral-500">Settle up — bill {id} (coming in Phase 3)</div>
  )
}
