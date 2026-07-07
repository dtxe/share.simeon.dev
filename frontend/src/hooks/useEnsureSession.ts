import { useCallback, useRef } from 'react'
import { useLocation } from 'wouter'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

// Lazily creates a bill session on the first meaningful user action (file
// picked, first person/item committed, total paid saved) rather than on
// every load of `/` — create-session is per-IP rate-limited, and eagerly
// creating one per page view would spam empty rows.
export function useEnsureSession(routeId: string | undefined) {
  const inflight = useRef<Promise<string> | null>(null)
  const [, navigate] = useLocation()
  const qc = useQueryClient()

  const ensure = useCallback((): Promise<string> => {
    if (routeId) return Promise.resolve(routeId)
    if (!inflight.current) {
      inflight.current = api
        .createSession()
        .then((sess) => {
          qc.invalidateQueries({ queryKey: ['myBills'] })
          navigate(`/bill/${sess.id}`, { replace: true })
          return sess.id
        })
        .catch((err) => {
          inflight.current = null
          throw err
        })
    }
    return inflight.current
  }, [routeId, navigate, qc])

  return ensure
}
