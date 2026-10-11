/** Amber priority bars from the Cycle 19 board-card prototype: an 11x11px
 *  flex box (gap 1px, items-end) holding three 2px-wide bars, 3/6/9px tall
 *  with 1px radius. Bars fill amber (#e7ad42) up to the priority level
 *  (0-3); unfilled bars render as a dim track so the level stays readable.
 *  Decorative — always paired with a text label, hence aria-hidden. */
export default function PriorityBars({
  priority,
  className,
}: {
  priority: number;
  className?: string;
}) {
  const filled = Math.max(0, Math.min(3, priority));
  return (
    <span
      aria-hidden="true"
      className={`inline-flex h-[11px] w-[11px] flex-none items-end justify-center gap-[1px] ${className ?? ""}`}
    >
      {[3, 6, 9].map((h, i) => (
        <span
          key={h}
          className="w-[2px] rounded-[1px]"
          style={{
            height: `${h}px`,
            backgroundColor: i < filled ? "#e7ad42" : "#3f3f46",
          }}
        />
      ))}
    </span>
  );
}
