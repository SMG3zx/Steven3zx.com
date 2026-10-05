export type RetrievalMetricInput = {
  route: string;
  methods: string[];
  durationMs: number;
  ok: boolean;
  estimatedCostUsd?: number;
};

const MAX_LATENCY_SAMPLES = 1_000;
const MAX_HISTORY_BUCKETS = 60;

type MetricsBucket = {
  minute: number;
  retrievals: number;
  errors: number;
  latencySumMs: number;
  latencySamples: number;
  validationRejected: number;
};

export class MetricsRegistry {
  private readonly startedAt = Date.now();
  private retrievalCount = 0;
  private retrievalErrors = 0;
  private answerValidationCount = 0;
  private answerValidationRejected = 0;
  private readonly routes = new Map<string, number>();
  private readonly methods = new Map<string, number>();
  private readonly latencies: number[] = [];
  private readonly history = new Map<number, MetricsBucket>();
  private estimatedCostUsd = 0;

  private bucket() {
    const minute = Math.floor(Date.now() / 60_000) * 60_000;
    const existing = this.history.get(minute);
    if (existing) return existing;
    const created: MetricsBucket = { minute, retrievals: 0, errors: 0, latencySumMs: 0, latencySamples: 0, validationRejected: 0 };
    this.history.set(minute, created);
    while (this.history.size > MAX_HISTORY_BUCKETS) this.history.delete(this.history.keys().next().value as number);
    return created;
  }

  recordRetrieval(input: RetrievalMetricInput) {
    this.retrievalCount += 1;
    this.estimatedCostUsd += Math.max(0, Number(input.estimatedCostUsd) || 0);
    if (!input.ok) this.retrievalErrors += 1;
    this.routes.set(input.route, (this.routes.get(input.route) ?? 0) + 1);
    for (const method of input.methods) this.methods.set(method, (this.methods.get(method) ?? 0) + 1);
    this.latencies.push(Math.max(0, Number(input.durationMs) || 0));
    if (this.latencies.length > MAX_LATENCY_SAMPLES) this.latencies.shift();
    const bucket = this.bucket();
    bucket.retrievals += 1;
    if (!input.ok) bucket.errors += 1;
    bucket.latencySumMs += Math.max(0, Number(input.durationMs) || 0);
    bucket.latencySamples += 1;
  }

  recordAnswerValidation(valid: boolean) {
    this.answerValidationCount += 1;
    if (!valid) this.answerValidationRejected += 1;
    if (!valid) this.bucket().validationRejected += 1;
  }

  snapshot() {
    const sorted = [...this.latencies].sort((left, right) => left - right);
    const sum = sorted.reduce((total, value) => total + value, 0);
    const retrievals = {
      count: this.retrievalCount,
      errors: this.retrievalErrors,
      errorRate: this.retrievalCount ? this.retrievalErrors / this.retrievalCount : 0,
      meanLatencyMs: sorted.length ? sum / sorted.length : 0,
      p95LatencyMs: sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))] : 0,
      routes: Object.fromEntries(this.routes),
      methods: Object.fromEntries(this.methods),
      retainedLatencySamples: sorted.length,
      estimatedCostUsd: this.estimatedCostUsd,
    };
    const errorRateAlert = Number(process.env.METRICS_ERROR_RATE_ALERT ?? 0.1);
    const p95AlertMs = Number(process.env.METRICS_P95_ALERT_MS ?? 1_000);
    const alerts = [];
    if (retrievals.count >= 5 && retrievals.errorRate >= errorRateAlert) alerts.push({ kind: "retrieval-error-rate", message: "Retrieval error rate is " + (retrievals.errorRate * 100).toFixed(1) + "% (threshold " + (errorRateAlert * 100).toFixed(1) + "%)." });
    if (retrievals.count >= 5 && retrievals.p95LatencyMs >= p95AlertMs) alerts.push({ kind: "retrieval-p95-latency", message: "Retrieval p95 latency is " + retrievals.p95LatencyMs.toFixed(1) + " ms (threshold " + p95AlertMs + " ms)." });
    return {
      uptimeSeconds: Math.round((Date.now() - this.startedAt) / 1000),
      retrievals,
      answerValidation: {
        count: this.answerValidationCount,
        rejected: this.answerValidationRejected,
        rejectionRate: this.answerValidationCount ? this.answerValidationRejected / this.answerValidationCount : 0,
      },
      alerts,
      trend: [...this.history.values()].sort((a, b) => a.minute - b.minute).map((bucket) => ({
        minute: new Date(bucket.minute).toISOString(),
        retrievals: bucket.retrievals,
        errors: bucket.errors,
        meanLatencyMs: bucket.latencySamples ? bucket.latencySumMs / bucket.latencySamples : 0,
        validationRejected: bucket.validationRejected,
      })),
    };
  }
}
