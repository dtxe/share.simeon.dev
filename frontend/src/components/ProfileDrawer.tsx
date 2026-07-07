import { useEffect } from 'react'
import { useLocation } from 'wouter'
import { Drawer } from 'vaul'
import { useQueryClient } from '@tanstack/react-query'
import { useMe, useInvalidateMe } from '../auth/useMe'
import { useAuthActions } from '../auth/useAuthActions'
import { api } from '../lib/api'
import { Button } from './ui/Button'

export function ProfileDrawer({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { data: me } = useMe()
  const invalidateMe = useInvalidateMe()
  const qc = useQueryClient()
  const [, navigate] = useLocation()
  const auth = useAuthActions()

  // vaul's Drawer.Root doesn't remount on close/reopen — only the portal's
  // visibility toggles — so stage/email/code state must be reset explicitly
  // whenever the drawer opens, or it shows stale data from the last visit.
  useEffect(() => {
    if (open) auth.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  async function logout() {
    await api.logout()
    await invalidateMe()
    qc.invalidateQueries({ queryKey: ['myBills'] })
    onOpenChange(false)
    navigate('/')
  }

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-black/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 rounded-t-2xl bg-white p-6 pb-8">
          <Drawer.Title className="mb-4 text-base font-semibold">Your account</Drawer.Title>

          <div className="mb-4 flex flex-col gap-1 text-sm">
            <span>Email — {me?.email ?? 'not linked'}</span>
            {me?.passkeysEnabled && <span>Passkey — {me.hasPasskey ? 'added' : 'none'}</span>}
          </div>

          {auth.stage === 'idle' && (
            <div className="flex flex-col gap-2">
              <Button variant="secondary" onClick={() => auth.setStage('email')}>
                Add email
              </Button>
              {auth.passkeysSupported && (
                <>
                  <Button variant="secondary" disabled={auth.busy !== null} onClick={() => void auth.runPasskey('register')}>
                    Add passkey
                  </Button>
                  <Button variant="secondary" disabled={auth.busy !== null} onClick={() => void auth.runPasskey('login')}>
                    Use passkey
                  </Button>
                </>
              )}
              <Button variant="destructive" onClick={() => void logout()}>
                Log out
              </Button>
            </div>
          )}

          {auth.stage === 'email' && (
            <div className="flex gap-2">
              <input
                type="email"
                inputMode="email"
                placeholder="you@example.com"
                value={auth.email}
                onChange={(e) => auth.setEmail(e.target.value)}
                className="flex-1 rounded-md border border-[var(--color-border)] px-3 py-2"
              />
              <Button onClick={() => void auth.requestCode()}>Send code</Button>
            </div>
          )}

          {auth.stage === 'code' && (
            <div className="flex gap-2">
              <input
                type="text"
                inputMode="numeric"
                placeholder="6-digit code"
                value={auth.code}
                onChange={(e) => auth.setCode(e.target.value)}
                className="flex-1 rounded-md border border-[var(--color-border)] px-3 py-2"
              />
              <Button onClick={() => void auth.verifyCode()}>Verify</Button>
            </div>
          )}

          {auth.busy && auth.passkeysSupported && (
            <p className="mt-3 text-sm text-neutral-500">
              Working on {auth.busy === 'register' ? 'creating' : 'using'} your passkey…
            </p>
          )}
          {auth.error && <p className="mt-3 text-sm text-[var(--color-warn)]">{auth.error}</p>}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
