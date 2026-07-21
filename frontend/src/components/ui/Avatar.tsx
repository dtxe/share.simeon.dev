import { personColor, personForeground, initials } from '../../lib/colors'

export function Avatar({
  name,
  sortOrder,
  size = 40,
  filled = true,
}: {
  name: string
  sortOrder: number
  size?: number
  filled?: boolean
}) {
  const color = personColor(sortOrder)
  const foreground = personForeground(sortOrder)
  return (
    <span
      className="flex shrink-0 items-center justify-center rounded-full font-semibold"
      style={{
        width: size,
        height: size,
        fontSize: size * 0.38,
        background: filled ? color : 'transparent',
        color: filled ? foreground : color,
        border: filled ? 'none' : `2px solid ${color}`,
      }}
    >
      {initials(name)}
    </span>
  )
}
