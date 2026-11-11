/** File-safe name derived from a link title, e.g. "Qwik Docs" -> "qwik-docs". */
export function slugOf(title: string): string {
  return title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
