// Display-ID parsing ("ENG-123") for the command palette (Task 25).

export interface ParsedDisplayId {
  identifier: string;
  sequence: number;
}

// parseDisplayId parses "ENG-123" (case-insensitive, surrounding whitespace
// tolerated) into {identifier: "ENG", sequence: 123}. Returns null for
// anything that isn't an identifier-dash-positive-integer shape.
export function parseDisplayId(input: string): ParsedDisplayId | null {
  const m = /^([A-Za-z]{2,12})-(\d{1,9})$/.exec(input.trim());
  if (!m) return null;
  const sequence = Number(m[2]);
  if (!Number.isSafeInteger(sequence) || sequence < 1) return null;
  return { identifier: m[1].toUpperCase(), sequence };
}
