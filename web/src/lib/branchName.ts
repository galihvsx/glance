// Dev quick actions: git branch names derived from an issue.
//
// Format: `glance/{identifier-lowercased}-{slug}` where `slug` is the issue
// title slugified to at most MAX_SLUG_CHARS characters. Non-ASCII characters
// are stripped (not transliterated), runs of non-alphanumerics collapse to a
// single dash, and leading/trailing dashes are trimmed. An empty (or
// symbol-only, or non-ASCII-only) title yields just `glance/{identifier}`.

export const MAX_SLUG_CHARS = 40;

/** Slugify an issue title for use in a branch name. */
export function slugifyTitle(title: string): string {
  const ascii = title.replace(/[^\p{ASCII}]/gu, "");
  const slug = ascii
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  // Truncation can leave a trailing dash ("…word-"); trim it so the
  // branch never ends with one.
  return slug.slice(0, MAX_SLUG_CHARS).replace(/-+$/g, "");
}

/** Build the branch name for an issue, e.g.
 *  buildBranchName("GLC-123", "Short Slug") → "glance/glc-123-short-slug". */
export function buildBranchName(
  displayId: string,
  title: string,
): string {
  const id = displayId.toLowerCase();
  const slug = slugifyTitle(title);
  return slug ? `glance/${id}-${slug}` : `glance/${id}`;
}
