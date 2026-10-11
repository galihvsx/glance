// Debounce a value: the returned value lags `value` by `delay` ms,
// resetting the timer on every change. Used by the global command
// palette (C18T0) to debounce the cross-project search input.

import { useEffect, useState } from "react";

export function useDebouncedValue<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(t);
  }, [value, delay]);
  return debounced;
}
