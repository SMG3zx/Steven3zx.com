import data from "./generated-links.json";
import previewSlugs from "./previews.json";
import { slugOf } from "./slug";

export interface SavedLink {
  title: string;
  url: string;
  description?: string;
  tags: string[];
}

/**
 * Every saved link, read from `src/links.json`.
 * Edit that file and rebuild to change the list.
 */
export const links: SavedLink[] = data;

/** Sorted, de-duplicated tag names across all links. */
export const allTags: string[] = [
  ...new Set(links.flatMap((link) => link.tags)),
].sort((a, b) => a.localeCompare(b));

/**
 * Path to the screenshot of a link. Links that are not live yet, or have not been
 * captured, get the generic placeholder image.
 */
export function previewOf(link: SavedLink): string {
  const slug = slugOf(link.title);
  return previewSlugs.includes(slug)
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
