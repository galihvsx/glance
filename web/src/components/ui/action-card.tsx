import * as React from "react"
import { cn } from "cn"
import { Card } from "./card"

type ActionCardProps = Omit<
  React.ComponentProps<"div"> & { size?: "default" | "sm" },
  "onClick" | "role"
> & {
  /** What the card does. "link" navigates somewhere; "button" performs an action. */
  role: "link" | "button"
  /** Accessible name for screen readers. Falls back to the card's text content. */
  label?: string
  onActivate: () => void
  disabled?: boolean
}

/**
 * A Card that is operable by keyboard and screen readers: focusable via
 * Tab, activated with Enter or Space, with a visible focus ring. Use
 * role="link" for cards that navigate and role="button" for cards that
 * perform an action — a plain div with onClick is invisible to both.
 */
export function ActionCard({
  role,
  label,
  onActivate,
  disabled = false,
  className,
  children,
  ...rest
}: ActionCardProps) {
  function handleKeyDown(e: React.KeyboardEvent) {
    if (disabled) return
    if (e.key === "Enter" || e.key === " ") {
      // Space would otherwise scroll the page.
      e.preventDefault()
      onActivate()
    }
  }

  return (
    <Card
      role={role}
      tabIndex={disabled ? -1 : 0}
      aria-label={label}
      aria-disabled={disabled || undefined}
      onClick={() => {
        if (!disabled) onActivate()
      }}
      onKeyDown={handleKeyDown}
      className={cn(
        "cursor-pointer transition-colors hover:bg-accent",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
        disabled && "pointer-events-none opacity-50",
        className
      )}
      {...rest}
    >
      {children}
    </Card>
  )
}
