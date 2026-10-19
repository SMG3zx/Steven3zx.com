const units = ["B", "KB", "MB", "GB", "TB"];

export function bytes(n: number, digits = 1): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / 1024 ** i).toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

export function rate(n: number): string {
  return `${bytes(n)}/s`;
}

export function pct(n: number, digits = 0): string {
  return `${(Number.isFinite(n) ? n : 0).toFixed(digits)}%`;
}

export function duration(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

/** Severity bucket for a utilisation percentage, used to colour meters. */
export function level(p: number): "ok" | "warn" | "crit" {
  if (p >= 90) return "crit";
  if (p >= 70) return "warn";
  return "ok";
}
