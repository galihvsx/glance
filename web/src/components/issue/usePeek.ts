import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

const PEEK_PARAM = "peek";

/**
 * Peek-drawer state carried in the URL query string (?peek=<uuid>).
 * The route stays the same, so the list/board keeps its selection,
 * scroll position and filters while the drawer is open. Opening and
 * closing use `replace` so the browser back button skips the peek
 * state instead of re-opening the drawer.
 */
export function usePeekParam() {
  const [searchParams, setSearchParams] = useSearchParams();
  const peekUuid = searchParams.get(PEEK_PARAM);

  const openPeek = useCallback(
    (uuid: string) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.set(PEEK_PARAM, uuid);
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const closePeek = useCallback(() => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(PEEK_PARAM);
        return next;
      },
      { replace: true },
    );
  }, [setSearchParams]);

  return { peekUuid, openPeek, closePeek };
}
