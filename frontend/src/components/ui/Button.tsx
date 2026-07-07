import { type ButtonHTMLAttributes, forwardRef } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { twMerge } from 'tailwind-merge'

const button = cva(
  'inline-flex items-center justify-center gap-2 rounded-lg font-medium transition-colors disabled:opacity-40 disabled:pointer-events-none min-h-11 px-4',
  {
    variants: {
      variant: {
        primary: 'bg-[var(--color-accent)] text-white hover:brightness-95',
        secondary: 'bg-white border border-[var(--color-border)] text-[var(--color-ink)] hover:bg-neutral-50',
        ghost: 'text-[var(--color-accent)] hover:bg-[var(--color-accent-tint)]',
        destructive: 'text-red-600 hover:bg-red-50',
      },
      size: {
        default: 'text-sm',
        sm: 'min-h-9 px-3 text-xs',
      },
    },
    defaultVariants: { variant: 'primary', size: 'default' },
  },
)

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof button> {}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, variant, size, ...props },
  ref,
) {
  return <button ref={ref} className={twMerge(button({ variant, size }), className)} {...props} />
})
