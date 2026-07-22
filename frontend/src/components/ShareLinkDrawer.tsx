import { useState } from 'react'
import { Drawer } from 'vaul'
import { Check, Copy, RefreshCw } from 'lucide-react'

export function ShareLinkDrawer({
  open,
  onOpenChange,
  shareUrl,
  shareLinkExists,
  shareLinkAvailable,
  rotateConfirm,
  onRotateConfirmChange,
  onRotate,
  rotating,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  shareUrl: string | null
  shareLinkExists: boolean
  shareLinkAvailable: boolean
  rotateConfirm: boolean
  onRotateConfirmChange: (confirm: boolean) => void
  onRotate: () => void
  rotating: boolean
}) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    if (!shareUrl) return
    await navigator.clipboard.writeText(shareUrl)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  const confirmation = rotateConfirm && (
    <div className="mt-4 rounded-lg border border-warning bg-warning-soft p-3 text-sm text-warning">
      <p>This invalidates <strong>all previously shared URLs</strong>. Continue?</p>
      <div className="mt-3 flex gap-2">
        <button type="button" className="flex-1 rounded-lg border border-border px-3 py-2" onClick={() => onRotateConfirmChange(false)}>
          Cancel
        </button>
        <button type="button" className="flex-1 rounded-lg bg-warning px-3 py-2 font-medium text-white" disabled={rotating} onClick={onRotate}>
          Generate new link
        </button>
      </div>
    </div>
  )

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-overlay" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 rounded-t-2xl bg-surface p-6 pb-8">
          <Drawer.Title className="mb-4 text-base font-semibold">Share this split</Drawer.Title>
          {shareUrl && shareLinkAvailable ? (
            <>
              <div className="flex items-center gap-2 rounded-lg border border-border bg-surface px-4 py-3">
                <span className="min-w-0 flex-1 truncate font-receipt text-sm">{shareUrl}</span>
                <button type="button" onClick={copy} aria-label="Copy link">
                  {copied ? <Check size={18} className="text-primary" /> : <Copy size={18} />}
                </button>
              </div>
              {typeof navigator.share === 'function' && (
                <button type="button" onClick={() => navigator.share({ url: shareUrl })} className="mt-4 w-full rounded-lg bg-primary py-3 font-medium text-primary-foreground">
                  Share…
                </button>
              )}
              {confirmation ?? (
                <button type="button" className="mt-4 flex items-center gap-1 text-xs text-foreground-muted" onClick={() => onRotateConfirmChange(true)}>
                  <RefreshCw size={13} /> Generate a new link
                </button>
              )}
            </>
          ) : shareLinkExists ? (
            <>
              <p className="text-sm text-foreground-muted">A share link exists, but this older link cannot be displayed. Generate a new link to view it.</p>
              <button type="button" className="mt-4 text-xs text-primary" disabled={rotating} onClick={onRotate}>
                {rotating ? 'Generating link…' : 'Generate a new link'}
              </button>
            </>
          ) : (
            <p className="text-sm text-foreground-muted">Generating link…</p>
          )}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
