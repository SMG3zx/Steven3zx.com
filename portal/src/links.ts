import data from "./generated-links.json";
import previewSlugs from "./previews.json";
import { slugOf } from "./slug";

export interface SavedLink {
  title: string;
  url: string;
  description?: string;
  tags: string[];
  /** Set on links found by scanning this machine rather than written down. */
  discovered?: boolean;
}

/**
 * Every saved link, read from `src/generated-links.json`.
 * Edit `links.manual.json` or a project's `portal.json` and rebuild to change the list.
 * Pages served on this machine are found at request time instead; see `discover.ts`.
 */
export const links: SavedLink[] = data;

/** Sorted, de-duplicated tag names across the given links. */
export function tagsOf(list: SavedLink[]): string[] {
  return [...new Set(list.flatMap((link) => link.tags))].sort((a, b) =>
    a.localeCompare(b),
  );
}

/**
 * Path to the screenshot of a link. Links that are not live yet, or have not been
 * captured, get the generic placeholder image.
 */
export function previewOf(link: SavedLink): string {
  const slug = slugOf(link.title);
  // A discovered title is whatever the page calls itself, so it never names a screenshot.
  return !link.discovered && previewSlugs.includes(slug)
    ? `/previews/${slug}.png`
    : "/previews/placeholder.svg";
}

/** Hostname without a leading `www.`, used as a short label. */
export function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return url;
  }
}
