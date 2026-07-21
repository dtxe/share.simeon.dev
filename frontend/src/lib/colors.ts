export function personColor(index: number): string {
  return `var(--color-person-${index % 8})`
}

export function personForeground(index: number): string {
  return `var(--color-person-${index % 8}-foreground)`
}

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/)
  const first = parts[0]?.[0] ?? '?'
  const last = parts.length > 1 ? parts[parts.length - 1][0] : ''
  return (first + last).toUpperCase()
}
