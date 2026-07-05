import { useState } from 'react'
import { useInvalidateMe } from '../auth/useMe'

type Stage = 'closed' | 'email' | 'code'

export function SaveHistoryBanner() {
  const [stage, setStage] = useState<Stage>('closed')
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [dismissed, setDismissed] = useState(false)
  const invalidateMe = useInvalidateMe()

  if (dismissed) return null

  async function requestCode() {
    setError(null)
    const res = await fetch('/api/auth/otp/request', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      setError(body.error ?? 'could not send code')
      return
    }
    setStage('code')
  }

  async function verifyCode() {
    setError(null)
    const res = await fetch('/api/auth/otp/verify', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, code }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      setError(body.error ?? 'invalid code')
      return
    }
    invalidateMe()
    setStage('closed')
    setDismissed(true)
  }

  if (stage === 'closed') {
    return (
      <div className="flex items-center justify-between gap-3 rounded-lg border border-[var(--color-border)] bg-white px-4 py-3 text-sm">
        <span>Save your splits — add an email to get them back on another device.</span>
        <div className="flex shrink-0 gap-2">
          <button type="button" className="text-[var(--color-accent)] font-medium" onClick={() => setStage('email')}>
            Add email
          </button>
          <button type="button" className="text-neutral-400" onClick={() => setDismissed(true)}>
            Dismiss
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-white px-4 py-3 text-sm">
      {stage === 'email' ? (
        <div className="flex gap-2">
          <input
            type="email"
            inputMode="email"
            placeholder="you@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="flex-1 rounded-md border border-[var(--color-border)] px-3 py-2"
          />
          <button
            type="button"
            onClick={requestCode}
            className="rounded-md bg-[var(--color-accent)] px-4 py-2 font-medium text-white"
          >
            Send code
          </button>
        </div>
      ) : (
        <div className="flex gap-2">
          <input
            type="text"
            inputMode="numeric"
            placeholder="6-digit code"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            className="flex-1 rounded-md border border-[var(--color-border)] px-3 py-2"
          />
          <button
            type="button"
            onClick={verifyCode}
            className="rounded-md bg-[var(--color-accent)] px-4 py-2 font-medium text-white"
          >
            Verify
          </button>
        </div>
      )}
      {error && <p className="mt-2 text-[var(--color-warn)]">{error}</p>}
    </div>
  )
}
