import { useState } from 'react'
import { Drawer } from 'vaul'
import { Check, Copy } from 'lucide-react'

export function ShareLinkDrawer({
  open,
  onOpenChange,
  shareUrl,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  shareUrl: string | null
}) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    if (!shareUrl) return
    await navigator.clipboard.writeText(shareUrl)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-black/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 rounded-t-2xl bg-[var(--color-paper)] p-6 pb-8">
          <Drawer.Title className="mb-4 text-base font-semibold">Share this split</Drawer.Title>
          {shareUrl ? (
            <>
              <div className="flex items-center gap-2 rounded-lg border border-[var(--color-border)] px-4 py-3">
                <span className="min-w-0 flex-1 truncate font-receipt text-sm">{shareUrl}</span>
                <button type="button" onClick={copy} aria-label="Copy link">
                  {copied ? <Check size={18} className="text-[var(--color-accent)]" /> : <Copy size={18} />}
                </button>
              </div>
              {typeof navigator.share === 'function' && (
                <button
                  type="button"
                  onClick={() => navigator.share({ url: shareUrl })}
                  className="mt-4 w-full rounded-lg bg-[var(--color-accent)] py-3 font-medium text-white"
                >
                  Share…
                </button>
              )}
            </>
          ) : (
            <p className="text-sm text-neutral-500">Generating link…</p>
          )}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
