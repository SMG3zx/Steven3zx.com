// The sections the block dock is split into. Each block belongs to the section
// for its category. Groups is the section for saved groups. The icons are
// 24-unit SVG paths drawn with strokes.

import type { Category } from "../shared/catalog";

export interface Section {
  id: Category | "groups";
  title: string;
  blurb: string;
  color: string;
  icon: string;
}

export const SECTIONS: Section[] = [
  {
    id: "program",
    title: "Program",
    blurb: "Where a program starts, and the functions it declares.",
    color: "#5ad1c3",
    icon: '<path d="M7 5l12 7-12 7z"/>',
  },
  {
    id: "flow",
    title: "Flow",
    blurb: "Decide what runs, and how many times.",
    color: "#f2c14e",
    icon: '<path d="M4 12h6l4-5h6"/><path d="M4 12h6l4 5h6"/>',
  },
  {
    id: "values",
    title: "Values",
    blurb: "Numbers, text, and the things that compute them.",
    color: "#6aa8ff",
    icon: '<path d="M9 4l-2 16M17 4l-2 16M4 9h16M3 15h16"/>',
  },
  {
    id: "vars",
    title: "Variables",
    blurb: "Names for values that a program keeps.",
    color: "#b478e8",
    icon: '<path d="M4 5h16v14H4z"/><path d="M9 9l6 6M15 9l-6 6"/>',
  },
  {
    id: "lists",
    title: "Lists",
    blurb: "Ordered groups of values, read and changed by position.",
    color: "#7fd16a",
    icon: '<path d="M4 6h16M4 12h16M4 18h10"/>',
  },
  {
    id: "calls",
    title: "Calls",
    blurb: "Run your own functions and the standard library.",
    color: "#e06ad8",
    icon: '<path d="M9 4c-3 3-3 13 0 16M15 4c3 3 3 13 0 16"/>',
  },
  {
    id: "io",
    title: "Input / output",
    blurb: "Print text, and build text from values.",
    color: "#ffb457",
    icon: '<path d="M4 6h16v12H4z"/><path d="M7 10l3 2-3 2M12 14h5"/>',
  },
  {
    id: "groups",
    title: "Groups",
    blurb: "Blocks you saved together, ready to add again.",
    color: "#aebfd0",
    icon: '<path d="M4 4h7v7H4zM13 13h7v7h-7z"/>',
  },
];

export const SECTION_BY_ID = new Map<string, Section>(SECTIONS.map((s) => [s.id, s]));

export function sectionIcon(section: Section, size = 26): string {
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="${section.color}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${section.icon}</svg>`;
}
