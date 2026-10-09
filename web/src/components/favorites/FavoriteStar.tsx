// Star toggle button (C7T4): self-contained favorite toggle for an issue
// or project. Reads the shared favorites cache for its state, flips via
// mutation, and invalidates on settle. Amber fill when starred, theme
// tokens otherwise (dark-theme aware).
import { Star } from "lucide-react";
import { Button } from "../ui/button";
import {
  useIsStarred,
  useToggleFavorite,
  type FavoritableType,
} from "../../lib/favorites";
import { cn } from "../../lib/utils";

export default function FavoriteStar({
  type,
  id,
  title,
  className,
}: {
  type: FavoritableType;
  id: string;
  /** Accessible label override; defaults to Star/Unstar. */
  title?: string;
  className?: string;
}) {
  const starred = useIsStarred(type, id);
  const toggle = useToggleFavorite();

  return (
    <Button
      variant="ghost"
      size="sm"
      className={cn("gap-1.5", className)}
      disabled={toggle.isPending}
      onClick={() => toggle.mutate({ type, id, starred })}
      title={title ?? (starred ? "Unstar" : "Star")}
      aria-label={starred ? "Unstar" : "Star"}
      aria-pressed={starred}
    >
      <Star
        className={cn(
          "h-4 w-4",
          starred && "fill-amber-400 text-amber-400",
        )}
      />
    </Button>
  );
}
