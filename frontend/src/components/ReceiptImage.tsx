import { useState } from 'react'

export function ReceiptImage({ src, size = 64 }: { src: string; size?: number }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setExpanded(true)} className="shrink-0 overflow-hidden rounded-lg">
        <img src={src} alt="Receipt" style={{ width: size, height: size }} className="object-cover" />
      </button>
      {expanded && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-overlay-strong p-4"
          onClick={() => setExpanded(false)}
        >
          <img src={src} alt="Receipt" className="max-h-full max-w-full rounded-lg object-contain" />
        </div>
      )}
    </>
  )
}
