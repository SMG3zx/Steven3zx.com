const baseUrl = (process.env.UX_SMOKE_URL ?? "http://127.0.0.1:4781").replace(/\/$/, "");
const headers = { "content-type": "application/json" };
const suffix = crypto.randomUUID();
const scope = { userId: `ux-smoke-${suffix}`, projectId: `ux-smoke-project-${suffix}`, sessionId: `ux-smoke-session-${suffix}` };

async function request(path: string, init?: RequestInit) {
  const response = await fetch(`${baseUrl}${path}`, init);
  const data = (response.headers.get("content-type") ?? "").includes("application/json")
    ? await response.json().catch(() => ({}))
    : await response.text();
  return { response, data };
}

function requireCondition(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

async function main() {
  const status = await request("/status");
  requireCondition(status.response.ok && status.data, "status page unavailable");
  requireCondition(String(status.data).includes("Backup and restore") && String(status.data).includes("RESTORE_DRY_RUN") && String(status.data).includes("RESTORE_CONFIRM"), "status recovery guidance unavailable");
  const chatPage = await request("/chat");
  requireCondition(chatPage.response.ok && String(chatPage.data).includes("Memory scope"), "chat scope UI unavailable");
  for (const route of ["/", "/chat", "/ingest", "/sessions", "/imports", "/memories", "/jobs", "/status", "/metrics"]) {
    const page = route === "/chat" ? chatPage : await request(route);
    const html = String(page.data);
    requireCondition(page.response.ok && ["/chat", "/ingest", "/sessions", "/imports", "/memories", "/jobs", "/metrics", "/status", "/"].every((href) => html.includes(`href=\"${href}\"`)) && (html.match(/class=\"global-nav\"/g) ?? []).length === 1, `primary navigation is incomplete on ${route}`);
    requireCondition((html.match(/aria-current=\"page\"/g) ?? []).length === 1, `active navigation state is incomplete on ${route}`);
    requireCondition(html.includes("apiAccessKey") && html.includes("API access"), `API access control is missing on ${route}`);
    requireCondition(html.includes("target.origin!==location.origin"), `API access must not attach bearer keys to cross-origin requests on ${route}`);
    requireCondition(html.includes("Source lookup failed") && html.includes("Download failed"), `authenticated local API navigation is missing on ${route}`);
    requireCondition(!/\b(prompt|confirm|alert)\s*\(/.test(html), `native blocking dialog remains on ${route}`);
  }
  requireCondition(String(chatPage.data).includes("Waiting for ChatGPT login to finish") && String(chatPage.data).includes("Inspect trace"), "chat authentication recovery UI unavailable");
  requireCondition(String(chatPage.data).includes("spinner") && String(chatPage.data).includes("aria-live") && String(chatPage.data).includes("aria-busy") && String(chatPage.data).includes("captureTurn"), "chat working/capture feedback unavailable");
  requireCondition(!/target="_blank"[^>]*href="[^"]*traceId=/.test(String(chatPage.data)), "chat trace receipts must remain same-tab in the embedded browser");
  requireCondition(String(chatPage.data).includes("role=\"alertdialog\"") && String(chatPage.data).includes("Save correction") && !String(chatPage.data).includes("prompt(") && !String(chatPage.data).includes("confirm("), "chat evidence actions must use in-context dialogs");
  const ingestPage = await request("/ingest");
  requireCondition(ingestPage.response.ok && String(ingestPage.data).includes("Capture into memory"), "capture page unavailable");
  requireCondition(String(ingestPage.data).includes("eventForm") && String(ingestPage.data).includes("documentForm") && String(ingestPage.data).includes("conversationForm") && String(ingestPage.data).includes("Review event") && String(ingestPage.data).includes("Confirm save") && String(ingestPage.data).includes("Retry") && String(ingestPage.data).includes("idempotencyKey") && String(ingestPage.data).includes("role=\"alertdialog\""), "capture paths, source review, idempotency, or recovery feedback are missing from the web interface");
  const sessionPage = await request("/sessions");
  requireCondition(sessionPage.response.ok && String(sessionPage.data).includes("Session knowledge") && String(sessionPage.data).includes("summaryForm") && String(sessionPage.data).includes("reflectionForm") && String(sessionPage.data).includes("Confirm save"), "session summary/reflection capture is missing from the web interface");
  requireCondition(String(sessionPage.data).includes("aria-live") && String(sessionPage.data).includes("role=\"alertdialog\"") && String(sessionPage.data).includes("for=\"sessionId\"") && String(sessionPage.data).includes("idempotencyKey"), "session capture accessibility or retry safety feedback is missing");
  const importPage = await request("/imports");
  requireCondition(importPage.response.ok && String(importPage.data).includes("Import a document") && String(importPage.data).includes("type=\"file\"") && String(importPage.data).includes("Confirm import") && String(importPage.data).includes("role=\"alertdialog\"") && String(importPage.data).includes("maxFileBytes") && String(importPage.data).includes("too large for a safe import"), "document import preview, size guard, or recovery flow is missing from the web interface");
  const memoriesPage = await request("/memories");
  requireCondition(memoriesPage.response.ok && String(memoriesPage.data).includes("Memory review"), "memory review unavailable");
  requireCondition(String(memoriesPage.data).includes("role=\"alertdialog\"") && String(memoriesPage.data).includes("Correction saved and linked") && String(memoriesPage.data).includes("Archive memory"), "memory correction/retraction/archive dialog unavailable");
  requireCondition(!/target="_blank"[^>]*href="\//.test(String(memoriesPage.data)), "local provenance links must remain same-tab in the embedded browser");
  const jobsPage = await request("/jobs");
  requireCondition(jobsPage.response.ok && String(jobsPage.data).includes("Processing & consolidation"), "job review unavailable");
  requireCondition(String(jobsPage.data).includes("actionStatus") && String(jobsPage.data).includes("confirmAction") && String(jobsPage.data).includes("View source event"), "job action or source review feedback unavailable");
  requireCondition(String(jobsPage.data).includes("Confirm approval") && String(jobsPage.data).includes("Cancel"), "job approval confirmation unavailable");
  const metricsPage = await request("/metrics");
  requireCondition(metricsPage.response.ok && String(metricsPage.data).includes("System metrics") && String(metricsPage.data).includes("Redacted aggregate behavior"), "metrics dashboard unavailable");
  requireCondition(String(metricsPage.data).includes("Recent trend") && String(metricsPage.data).includes("No active thresholds."), "metrics trend and alert surface unavailable");
  const auditPage = await request("/");
  requireCondition(auditPage.response.ok && String(auditPage.data).includes("Filter audit status") && String(auditPage.data).includes("personal-memory-audit-filter"), "audit filtering UI unavailable");
  requireCondition(String(auditPage.data).includes("Trace ID (paste from chat receipt)") && String(auditPage.data).includes("Filter audit trace ID") && String(auditPage.data).includes("Copy trace ID"), "audit trace-ID search or copy affordance unavailable");

  const event = await request("/v1/events", { method: "POST", headers, body: JSON.stringify({ ...scope, kind: "artifact", content: `UX smoke source ${suffix}`, source: "ux-smoke" }) });
  requireCondition(event.response.ok && typeof event.data.traceId === "string", "event ingestion did not return a trace");
  requireCondition(typeof event.data.id === "string", "event ingestion did not return an event ID");
  const invalidEvent = await request("/v1/events", { method: "POST", headers, body: JSON.stringify({ ...scope, source: "ux-smoke" }) });
  requireCondition(invalidEvent.response.status === 400 && typeof invalidEvent.data.traceId === "string", "invalid event did not return a validation trace");

  const batch = await request("/v1/events/batch", { method: "POST", headers, body: JSON.stringify({ events: [{ ...scope, kind: "tool_result", content: "UX smoke batch", source: "ux-smoke" }] }) });
  requireCondition(batch.response.ok && typeof batch.data.traceId === "string", "batch ingestion did not return a trace");

  const conversation = await request("/v1/conversations", { method: "POST", headers, body: JSON.stringify({ ...scope, messages: [{ kind: "user_message", content: "UX smoke conversation", source: "ux-smoke" }] }) });
  requireCondition(conversation.response.ok && typeof conversation.data.traceId === "string", "conversation ingestion did not return a trace");
  const entity = await request("/v1/entities", { method: "POST", headers, body: JSON.stringify({ ...scope, name: "UX smoke entity "+suffix, type: "concept" }) });
  requireCondition(entity.response.ok && typeof entity.data.id === "string" && typeof entity.data.traceId === "string", "entity ingestion did not return a trace");
  const invalidEntity = await request("/v1/entities", { method: "POST", headers, body: JSON.stringify({ ...scope, type: "concept" }) });
  requireCondition(invalidEntity.response.status === 400 && typeof invalidEntity.data.traceId === "string", "invalid entity did not return a validation trace");
  const recallQuery = "UX smoke private query "+suffix;
  const recall = await request("/v1/recall", { method: "POST", headers, body: JSON.stringify({ ...scope, query: recallQuery, limit: 5 }) });
  requireCondition(recall.response.ok && typeof recall.data.traceId === "string", "recall did not return a trace");
  const recallTrace = await request("/v1/admin/audit/traces/"+encodeURIComponent(recall.data.traceId));
  requireCondition(recallTrace.response.ok && !String(recallTrace.data).includes(recallQuery), "recall trace leaked the raw query");
  const invalidContext = await request("/v1/context", { method: "POST", headers, body: JSON.stringify({ ...scope, query: "UX smoke context", maxChars: 100 }) });
  requireCondition(invalidContext.response.status === 400 && typeof invalidContext.data.traceId === "string", "invalid context did not return a validation trace");
  const answerValidation = await request("/v1/answers/validate", { method: "POST", headers, body: JSON.stringify({ query: "UX smoke answer", answer: "UX smoke source "+suffix+" [artifact:"+event.data.id+"]", results: [{ id: event.data.id, type: "artifact", content: "UX smoke source "+suffix }] }) });
  requireCondition(answerValidation.response.ok && answerValidation.data.valid === true && typeof answerValidation.data.traceId === "string", "answer validation did not return a trace");
  const invalidAnswerValidation = await request("/v1/answers/validate", { method: "POST", headers, body: JSON.stringify({ answer: "missing evidence" }) });
  requireCondition(invalidAnswerValidation.response.status === 400 && typeof invalidAnswerValidation.data.traceId === "string", "invalid answer validation did not return a trace");

  const document = await request("/v1/documents", { method: "POST", headers, body: JSON.stringify({ ...scope, title: "UX smoke document", source: "ux-smoke", content: "source" }) });
  requireCondition(document.response.ok && typeof document.data.traceId === "string", "document ingestion did not return a trace");
  const invalidDocument = await request("/v1/documents", { method: "POST", headers, body: JSON.stringify({ ...scope, title: "UX smoke invalid document" }) });
  requireCondition(invalidDocument.response.status === 400 && typeof invalidDocument.data.traceId === "string", "invalid document did not return a validation trace");

  const source = await request(`/v1/events/${encodeURIComponent(event.data.id)}?userId=${encodeURIComponent(scope.userId)}&projectId=${encodeURIComponent(scope.projectId)}`);
  requireCondition(source.response.ok && source.data.content === `UX smoke source ${suffix}`, "scoped source lookup failed");
  const leaked = await request(`/v1/events/${encodeURIComponent(event.data.id)}?userId=another-user`);
  requireCondition(leaked.response.status === 404, "source scope isolation failed");
  const wrongSession = await request(`/v1/events/${encodeURIComponent(event.data.id)}?userId=${encodeURIComponent(scope.userId)}&projectId=${encodeURIComponent(scope.projectId)}&sessionId=another-session`);
  requireCondition(wrongSession.response.status === 404, "source session isolation failed");

  const memory = await request("/v1/memories", { method: "POST", headers, body: JSON.stringify({ ...scope, content: `UX smoke memory ${suffix}`, category: "fact", sourceEventIds: [event.data.id] }) });
  requireCondition(memory.response.ok && typeof memory.data.id === "string" && typeof memory.data.traceId === "string", "memory creation did not return a trace");
  const invalidMemory = await request("/v1/memories", { method: "POST", headers, body: JSON.stringify({ ...scope, category: "fact" }) });
  requireCondition(invalidMemory.response.status === 400 && typeof invalidMemory.data.traceId === "string", "invalid memory did not return a validation trace");
  const invalidUtility = await request(`/v1/memories/${encodeURIComponent(memory.data.id)}/utility`, { method: "POST", headers, body: JSON.stringify({ feedback: "ux-smoke" }) });
  requireCondition(invalidUtility.response.status === 400 && typeof invalidUtility.data.traceId === "string", "invalid utility did not return a validation trace");
const feedbackSentinel = "UX_SMOKE_PRIVATE_FEEDBACK_"+suffix;
  const utility = await request(`/v1/memories/${encodeURIComponent(memory.data.id)}/utility`, { method: "POST", headers, body: JSON.stringify({ useful: true, feedback: feedbackSentinel }) });
  requireCondition(utility.response.ok && typeof utility.data.traceId === "string", "memory utility did not return a trace");
  const utilityTrace = await request("/v1/admin/audit/traces/"+encodeURIComponent(utility.data.traceId));
  requireCondition(utilityTrace.response.ok && !String(utilityTrace.data).includes(feedbackSentinel), "utility trace leaked raw feedback");
  const supersededMemory = await request("/v1/memories", { method: "POST", headers, body: JSON.stringify({ ...scope, content: `UX smoke prior correction ${suffix}`, category: "fact", sourceEventIds: [event.data.id] }) });
  requireCondition(supersededMemory.response.ok && typeof supersededMemory.data.id === "string", "supersession fixture memory was not created");
  const correction = await request("/v1/memories", { method: "POST", headers, body: JSON.stringify({ ...scope, content: `UX smoke corrected memory ${suffix}`, category: "fact", sourceEventIds: [event.data.id], supersedesMemoryId: supersededMemory.data.id }) });
  requireCondition(correction.response.ok && typeof correction.data.traceId === "string", "memory correction did not return a trace");
  const supersededList = await request(`/v1/memories?userId=${encodeURIComponent(scope.userId)}&projectId=${encodeURIComponent(scope.projectId)}&status=superseded`);
  requireCondition(supersededList.response.ok && (supersededList.data.memories ?? []).some((item: { id?: string; supersededByIds?: string[] }) => item.id === supersededMemory.data.id && item.supersededByIds?.includes(correction.data.id)), "supersession links were not visible in lifecycle review");
  const archiveMemory = await request("/v1/memories", { method: "POST", headers, body: JSON.stringify({ ...scope, content: `UX smoke archive ${suffix}`, category: "fact", sourceEventIds: [event.data.id] }) });
  requireCondition(archiveMemory.response.ok && typeof archiveMemory.data.id === "string", "archive fixture memory was not created");
  const archived = await request(`/v1/memories/${encodeURIComponent(archiveMemory.data.id)}/archive`, { method: "POST", headers, body: JSON.stringify({ reason: "ux-smoke archive cleanup" }) });
  requireCondition(archived.response.ok && typeof archived.data.traceId === "string", "memory archival did not return a trace");
  const archivedList = await request(`/v1/memories?userId=${encodeURIComponent(scope.userId)}&projectId=${encodeURIComponent(scope.projectId)}&status=archived`);
  requireCondition(archivedList.response.ok && (archivedList.data.memories ?? []).some((item: { id?: string }) => item.id === archiveMemory.data.id), "archived memory was not visible in lifecycle review");
  const retracted = await request(`/v1/memories/${encodeURIComponent(memory.data.id)}/retract`, { method: "POST", headers, body: JSON.stringify({ reason: "ux-smoke cleanup" }) });
  requireCondition(retracted.response.ok && typeof retracted.data.traceId === "string", "memory retraction did not return a trace");

  const memories = await request(`/v1/memories?userId=${encodeURIComponent(scope.userId)}&projectId=${encodeURIComponent(scope.projectId)}&limit=20`);
  requireCondition(memories.response.ok, "scoped memory review API failed");
  const jobs = await request("/v1/jobs?limit=20");
  requireCondition(jobs.response.ok && Array.isArray(jobs.data.jobs), "job review API failed");
  const audit = await request("/v1/admin/audit/runs?limit=200");
  requireCondition(audit.response.ok && (audit.data.runs ?? []).some((run: { traceId?: string }) => run.traceId === event.data.traceId), "ingestion trace is not visible in audit");
  const traceSearch = await request(`/v1/admin/audit/runs?limit=10&name=${encodeURIComponent(event.data.traceId)}`);
  requireCondition(traceSearch.response.ok && (traceSearch.data.runs ?? []).some((run: { traceId?: string }) => run.traceId === event.data.traceId), "audit trace ID search did not find the receipt");

  console.log(JSON.stringify({
    ok: true,
    scenarios: ["status", "chat-scope", "chat-auth-recovery", "chat-working-feedback", "chat-trace-navigation", "memory-review", "memory-dialogs", "memory-supersession", "memory-archive", "job-review", "job-action-feedback", "metrics-dashboard", "audit-filters", "audit-trace-id-search", "event-receipt", "ingress-validation-receipts", "batch-receipt", "conversation-receipt", "entity-receipt", "retrieval-receipts", "answer-validation", "document-receipt", "source-provenance", "scope-isolation", "session-source-isolation", "memory-action-receipts", "memory-validation-receipts", "audit-discoverability"],
    traces: [event.data.traceId, batch.data.traceId, conversation.data.traceId, entity.data.traceId, recall.data.traceId, answerValidation.data.traceId, document.data.traceId, memory.data.traceId, utility.data.traceId, correction.data.traceId, archived.data.traceId, retracted.data.traceId],
  }, null, 2));
}

await main();
