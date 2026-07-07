import { useState } from 'react'
import { useInvalidateMe } from './useMe'
import { api, ApiError } from '../lib/api'
import { createPasskey, getPasskeyAssertion, supportsPasskeys } from '../lib/passkey'

export type OtpStage = 'idle' | 'email' | 'code'
export type PasskeyBusy = 'register' | 'login' | null

export function useAuthActions() {
  const [stage, setStage] = useState<OtpStage>('idle')
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<PasskeyBusy>(null)
  const invalidateMe = useInvalidateMe()

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
      await invalidateMe()
      setStage('idle')
      setEmail('')
      setCode('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'invalid code')
    }
  }

  async function runPasskey(mode: Exclude<PasskeyBusy, null>) {
    setBusy(mode)
    setError(null)
    try {
      if (mode === 'register') {
        await createPasskey()
      } else {
        await getPasskeyAssertion()
      }
      await invalidateMe()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'passkey flow failed')
    } finally {
      setBusy(null)
    }
  }

  function reset() {
    setStage('idle')
    setEmail('')
    setCode('')
    setError(null)
    setBusy(null)
  }

  return {
    stage,
    setStage,
    email,
    setEmail,
    code,
    setCode,
    error,
    busy,
    requestCode,
    verifyCode,
    runPasskey,
    reset,
    supportsPasskeys: supportsPasskeys(),
  }
}
