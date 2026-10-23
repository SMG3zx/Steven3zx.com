import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type { LogLine } from "./types";

/** Live container output. The dashboard owns the lines; this view filters and scrolls them. */
export function LogView({ lines, notice }: { lines: LogLine[]; notice: string }) {
  const [filter, setFilter] = useState("");
  const [stderrOnly, setStderrOnly] = useState(false);
  const [follow, setFollow] = useState(true);
  const box = useRef<HTMLDivElement>(null);

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return lines.filter((l) => (!stderrOnly || l.stream === "stderr") && (!q || l.text.toLowerCase().includes(q)));
  }, [lines, filter, stderrOnly]);

  // Keep the newest line in view while following. Scrolling up stops following.
  useLayoutEffect(() => {
    const el = box.current;
    if (follow && el) el.scrollTop = el.scrollHeight;
  }, [shown, follow]);

  function onScroll() {
    const el = box.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    if (!atBottom && follow) setFollow(false);
  }

  return (
    <div className="logview">
      <div className="log-toolbar">
        <input
          className="filter"
          placeholder={`Filter ${lines.length} lines…`}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <label className="small">
          <input type="checkbox" checked={stderrOnly} onChange={(e) => setStderrOnly(e.target.checked)} /> stderr
        </label>
        <button className="ghost" aria-pressed={follow} onClick={() => setFollow((f) => !f)}>
          {follow ? "Following" : "Follow"}
        </button>
      </div>
      {notice && <div className="log-notice small">{notice}</div>}
      <div className="log-lines mono" ref={box} onScroll={onScroll}>
        {shown.length === 0 ? (
          <div className="muted small">{lines.length === 0 ? "Waiting for output…" : "No lines match."}</div>
        ) : (
          shown.map((l, i) => (
            <div key={i} className={`log-line ${l.stream}`}>
              <span className="log-time">{clock(l.time)}</span>
              <span className="log-text">{l.text}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

/** Local clock time for a Docker timestamp. Docker's nanoseconds fall back to the raw HH:MM:SS. */
function clock(time?: string) {
  if (!time) return "";
  const d = new Date(time);
  return Number.isNaN(d.getTime()) ? time.slice(11, 19) : d.toLocaleTimeString();
}
