import { useState } from 'react'
import { useInvalidateMe } from '../auth/useMe'
import { api, ApiError } from '../lib/api'
import { createPasskey, getPasskeyAssertion, supportsPasskeys } from '../lib/passkey'

type Stage = 'closed' | 'email' | 'code'
type Busy = 'register' | 'login' | null

export function SaveHistoryBanner() {
  const [stage, setStage] = useState<Stage>('closed')
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [dismissed, setDismissed] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const invalidateMe = useInvalidateMe()

  if (dismissed) return null

  async function runPasskey(mode: Exclude<Busy, null>) {
    setBusy(mode)
    setError(null)
    try {
      if (mode === 'register') {
        await createPasskey()
      } else {
        await getPasskeyAssertion()
      }
      await invalidateMe()
      setStage('closed')
      setDismissed(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'passkey flow failed')
    } finally {
      setBusy(null)
    }
  }

  async function requestCode() {
    setError(null)
    try {
      await api.requestOtp(email)
      setStage('code')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'could not send code')
    }
  }

  async function verifyCode() {
    setError(null)
    try {
      await api.verifyOtp(email, code)
      invalidateMe()
      setStage('closed')
      setDismissed(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'invalid code')
    }
  }

  if (stage === 'closed') {
    return (
      <div className="flex items-center justify-between gap-3 rounded-lg border border-[var(--color-border)] bg-white px-4 py-3 text-sm">
        <span>Save your splits - add an email or a passkey to get them back on another device.</span>
        <div className="flex shrink-0 flex-wrap gap-2">
          <button type="button" className="text-[var(--color-accent)] font-medium" onClick={() => setStage('email')}>
            Add email
          </button>
          {supportsPasskeys() && (
            <>
              <button type="button" className="text-[var(--color-accent)] font-medium" onClick={() => void runPasskey('register')} disabled={busy !== null}>
                Add passkey
              </button>
              <button type="button" className="text-[var(--color-accent)] font-medium" onClick={() => void runPasskey('login')} disabled={busy !== null}>
                Use passkey
              </button>
            </>
          )}
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
      {busy && <p className="mt-2 text-neutral-500">Working on {busy === 'register' ? 'creating' : 'using'} your passkey...</p>}
      {error && <p className="mt-2 text-[var(--color-warn)]">{error}</p>}
    </div>
  )
}
