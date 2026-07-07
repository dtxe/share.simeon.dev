import { useState } from 'react'
import { Link } from 'wouter'
import { History, User } from 'lucide-react'
import { ProfileDrawer } from './ProfileDrawer'

export function AppHeader() {
  const [profileOpen, setProfileOpen] = useState(false)
  return (
    <header className="flex items-center justify-between px-1 py-3">
      <Link href="/" className="text-lg font-semibold">
        Share
      </Link>
      <div className="flex items-center gap-1">
        <Link
          href="/history"
          aria-label="History"
          className="flex h-10 w-10 items-center justify-center rounded-full hover:bg-neutral-100"
        >
          <History size={20} />
        </Link>
        <button
          type="button"
          aria-label="Profile"
          onClick={() => setProfileOpen(true)}
          className="flex h-10 w-10 items-center justify-center rounded-full hover:bg-neutral-100"
        >
          <User size={20} />
        </button>
      </div>
      <ProfileDrawer open={profileOpen} onOpenChange={setProfileOpen} />
    </header>
  )
}
