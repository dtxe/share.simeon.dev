import { useSyncExternalStore } from 'react'

export type ThemePreference = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'share-theme'
const listeners = new Set<() => void>()

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

let currentPreference = readThemePreference()
applyThemePreference(currentPreference)

function notify(): void {
  listeners.forEach((listener) => listener())
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  if (listeners.size === 1 && typeof window !== 'undefined') {
    window.addEventListener('storage', handleStorage)
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0 && typeof window !== 'undefined') {
      window.removeEventListener('storage', handleStorage)
    }
  }
}

function handleStorage(event: StorageEvent): void {
  if (event.key !== STORAGE_KEY) return
  currentPreference = isThemePreference(event.newValue) ? event.newValue : 'system'
  applyThemePreference(currentPreference)
  notify()
}

function persistThemePreference(preference: ThemePreference): void {
  try {
    if (typeof window !== 'undefined') window.localStorage.setItem(STORAGE_KEY, preference)
  } catch {
    // Storage can be unavailable in private/restricted browsing contexts.
  }
}

function setThemePreference(preference: ThemePreference): void {
  if (preference === currentPreference) {
    applyThemePreference(preference)
    return
  }
  currentPreference = preference
  applyThemePreference(preference)
  persistThemePreference(preference)
  notify()
}

export function useThemePreference() {
  const preference = useSyncExternalStore(subscribe, () => currentPreference, (): ThemePreference => 'system')

  return { preference, setPreference: setThemePreference }
}
