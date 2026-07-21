import { useEffect, useState } from 'react'

export type ThemePreference = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'share-theme'

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function readThemePreference(): ThemePreference {
  try {
    if (typeof window === 'undefined') return 'system'
    const value = window.localStorage.getItem(STORAGE_KEY)
    return isThemePreference(value) ? value : 'system'
  } catch {
    return 'system'
  }
}

export function applyThemePreference(preference: ThemePreference): void {
  try {
    if (typeof document === 'undefined') return
    if (preference === 'system') document.documentElement.removeAttribute('data-theme')
    else document.documentElement.setAttribute('data-theme', preference)
  } catch {
    // A restricted document should not prevent the application from loading.
  }
}

function persistThemePreference(preference: ThemePreference): void {
  try {
    if (typeof window !== 'undefined') window.localStorage.setItem(STORAGE_KEY, preference)
  } catch {
    // Storage can be unavailable in private/restricted browsing contexts.
  }
}

export function useThemePreference() {
  const [preference, setPreference] = useState<ThemePreference>(() => readThemePreference())

  useEffect(() => {
    applyThemePreference(preference)
    persistThemePreference(preference)
  }, [preference])

  return {
    preference,
    setPreference: (next: ThemePreference) => setPreference(next),
  }
}
