// The guided tour in the browser. A card explains one step and points at the
// part of the editor it is about; a ring draws round that part and dims the
// rest. Steps with a check move on by themselves once the user does the thing.
// The steps and checks are in shared/tour.ts.

import { TOUR_STEPS, type TourFacts, type TourStep } from "../shared/tour";

const KEY = "gnode.tour";
const TICK_MS = 250;
const ADVANCE_MS = 700;

interface Saved {
  step: number;
  done: boolean;
}

export interface TourHost {
  facts(): TourFacts;
  newProgram(): void;
}

export interface Tour {
  // Starts from the beginning, whatever the saved progress.
  start(): void;
  // Resumes an unfinished tour, or starts one for a first visit.
  startIfNew(): void;
}

// Progress is kept in this browser only. Storage can be blocked, in which
// case the tour still works, it just does not remember where you were.
function load(): Saved | null {
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? "null") as Saved | null;
  } catch {
    return null;
  }
}

function save(s: Saved) {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    // Without storage the tour simply will not resume after a reload.
  }
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, id?: string, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (id) e.id = id;
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

export function createTour(host: TourHost, steps: TourStep[] = TOUR_STEPS): Tour {
  let built: {
    ring: HTMLElement;
    card: HTMLElement;
    count: HTMLElement;
    title: HTMLElement;
    text: HTMLElement;
    status: HTMLElement;
    back: HTMLButtonElement;
    offer: HTMLButtonElement;
    next: HTMLButtonElement;
    close: HTMLButtonElement;
  } | null = null;

  let active = false;
  let index = 0;
  // The facts when the current step began.
  let entry: TourFacts | null = null;
  // Auto-advance only after the check has been seen false on this step, so a
  // step that is already done when reached waits for Next instead of skipping.
  let sawFalse = false;
  let advancing = false;
  let timer: ReturnType<typeof setInterval> | undefined;

  function build() {
    const ring = el("div", undefined, "tour-ring hidden");
    const card = el("div", undefined, "tour-card hidden");
    const head = el("div", undefined, "tc-head");
    const count = el("span", undefined, "tc-count");
    const title = el("b", undefined, "tc-title");
    head.append(count, title);
    const text = el("p", undefined, "tc-text");
    const status = el("p", undefined, "tc-status");
    const foot = el("div", undefined, "tc-foot");
    const close = el("button", undefined, "tc-close", "Close");
    const back = el("button", undefined, "tc-back", "Back");
    const offer = el("button", undefined, "tc-offer", "Start on a new program");
    const next = el("button", undefined, "tc-next primary", "Next");
    offer.type = back.type = next.type = close.type = "button";
    foot.append(close, back, offer, next);
    card.append(head, text, status, foot);
    document.body.append(ring, card);

    close.addEventListener("click", () => stop(true));
    back.addEventListener("click", () => {
      if (index > 0) show(index - 1);
    });
    next.addEventListener("click", () => forward());
    offer.addEventListener("click", () => {
      host.newProgram();
      forward();
    });
    window.addEventListener("resize", () => place());
    window.addEventListener("keydown", (e) => {
      if (!active || e.key !== "Escape") return;
      if ((e.target as HTMLElement).closest("input, select, textarea")) return;
      stop(true);
    });

    built = { ring, card, count, title, text, status, back, offer, next, close };
  }

  function current(): TourStep {
    return steps[index];
  }

  function show(i: number) {
    if (!built) return;
    index = i;
    const step = current();
    entry = host.facts();
    // A step that is not done yet moves on as soon as its check comes true.
    sawFalse = !!step.check && !step.check(entry, entry);
    advancing = false;
    save({ step: index, done: false });

    const isLast = index >= steps.length - 1;
    built.count.textContent = `Step ${index + 1} of ${steps.length}`;
    built.title.textContent = step.title;
    built.text.textContent = step.text;
    built.back.hidden = index === 0;
    built.offer.hidden = step.offer !== "newProgram";
    built.next.textContent = isLast ? "Finish" : "Next";
    built.card.classList.remove("hidden");
    refreshStatus();
    place();
  }

  function refreshStatus() {
    if (!built || !active) return;
    const step = current();
    if (advancing) {
      built.status.textContent = "Done";
      built.status.className = "tc-status done";
    } else if (step.check && entry && step.check(host.facts(), entry)) {
      built.status.textContent = "Already done. Press Next to carry on.";
      built.status.className = "tc-status";
    } else if (step.check) {
      built.status.textContent = step.hint ?? "";
      built.status.className = "tc-status waiting";
    } else {
      built.status.textContent = "";
      built.status.className = "tc-status";
    }
  }

  function forward() {
    if (index >= steps.length - 1) {
      stop(true);
      return;
    }
    show(index + 1);
  }

  function targetRect(): DOMRect | null {
    const sel = current().target;
    if (!sel) return null;
    const node = document.querySelector<HTMLElement>(sel);
    if (!node) return null;
    const r = node.getBoundingClientRect();
    return r.width > 0 && r.height > 0 ? r : null;
  }

  // Puts the ring round the target, and the card beside it when there is room,
  // otherwise on the other side or in the middle of the screen.
  function place() {
    if (!built || !active) return;
    const { ring, card } = built;
    const rect = targetRect();
    if (!rect) {
      ring.classList.add("hidden");
      card.style.left = `${Math.max(12, (innerWidth - card.offsetWidth) / 2)}px`;
      card.style.top = `${Math.max(12, (innerHeight - card.offsetHeight) / 2)}px`;
      return;
    }
    const pad = 6;
    Object.assign(ring.style, {
      left: `${rect.left - pad}px`,
      top: `${rect.top - pad}px`,
      width: `${rect.width + pad * 2}px`,
      height: `${rect.height + pad * 2}px`,
    });
    ring.classList.remove("hidden");

    // Try each side of the target and keep the first spot on screen that does
    // not cover the target.
    const gap = 16, cw = card.offsetWidth, ch = card.offsetHeight;
    const clampX = (x: number) => Math.max(12, Math.min(x, innerWidth - cw - 12));
    const clampY = (y: number) => Math.max(12, Math.min(y, innerHeight - ch - 12));
    const spots = [
      { x: rect.right + gap, y: clampY(rect.top) },
      { x: rect.left - cw - gap, y: clampY(rect.top) },
      { x: clampX(rect.left), y: rect.bottom + gap },
      { x: clampX(rect.left), y: rect.top - ch - gap },
    ];
    const onScreen = (s: { x: number; y: number }) =>
      s.x >= 12 && s.y >= 12 && s.x + cw <= innerWidth - 12 && s.y + ch <= innerHeight - 12;
    const clear = (s: { x: number; y: number }) =>
      s.x + cw <= rect.left || s.x >= rect.right || s.y + ch <= rect.top || s.y >= rect.bottom;
    const spot = spots.find((s) => onScreen(s) && clear(s)) ?? spots.find(onScreen) ?? spots[0];
    card.style.left = `${clampX(spot.x)}px`;
    card.style.top = `${clampY(spot.y)}px`;
  }

  // Runs on a timer: refreshes the card and moves on when a check has come true.
  function tick() {
    if (!active || !built) return;
    const step = current();
    if (step.check && entry && !advancing) {
      const ok = step.check(host.facts(), entry);
      if (!ok) sawFalse = true;
      else if (sawFalse) {
        advancing = true;
        const at = index;
        setTimeout(() => {
          if (active && index === at) forward();
        }, ADVANCE_MS);
      }
    }
    refreshStatus();
    place();
  }

  function stop(finished: boolean) {
    active = false;
    if (timer !== undefined) clearInterval(timer);
    timer = undefined;
    if (built) {
      built.card.classList.add("hidden");
      built.ring.classList.add("hidden");
    }
    if (finished) save({ step: index, done: true });
  }

  function begin(from: number) {
    if (!built) build();
    active = true;
    if (timer === undefined) timer = setInterval(tick, TICK_MS);
    show(Math.max(0, Math.min(from, steps.length - 1)));
  }

  return {
    start() {
      begin(0);
    },
    startIfNew() {
      const s = load();
      if (!s) begin(0);
      else if (!s.done) begin(s.step);
    },
  };
}
