import { Neo4jStore } from "./neo4j-store";
import { ContentTooLargeError } from "./content-limits";
import { buildContextPack } from "./context";
import { validateCitationLockedAnswer } from "./citations";
import { FaithfulnessJudge } from "./faithfulness";
import { MetricsRegistry } from "./metrics";
import { probeProvider } from "./provider-status";
import { AuditRegistry } from "./audit";
import { auditConsoleHtml } from "./audit-ui";
import { CodexAppServerClient } from "./codex-app-server";
import { chatHtml } from "./chat-ui";
import { memoryLibraryHtml } from "./memory-ui";
import { jobsHtml } from "./jobs-ui";
import { createConsolidator } from "./consolidation";
import { statusHtml } from "./status-ui";
import { metricsHtml } from "./metrics-ui";
import { ingestHtml } from "./ingest-ui";
import { sessionHtml } from "./session-ui";
import { importHtml } from "./import-ui";
import { assessEvidence, correctiveQuery, retrievalPlan, routeQuery, shouldAbstain } from "./retrieval";
import type { RetrievalRoute } from "./retrieval";
import type { ClaimInput, ChunkInput, ConversationInput, CuratedMemory, DocumentInput, Entity, MemoryEvent, ProcedureInput, RetrievalMethod, SessionReflectionInput, SessionSummaryInput } from "./types";

const store = new Neo4jStore();
const metrics = new MetricsRegistry();
export const audit = new AuditRegistry();
await audit.load();
const codex = new CodexAppServerClient();
const consolidator = createConsolidator();
await store.ensureSchema();
const host = process.env.MEMORY_HOST ?? "127.0.0.1";
const port = Number(process.env.MEMORY_PORT ?? 4781);
const maxBodyBytes = Number(process.env.MEMORY_MAX_BODY_BYTES ?? 4 * 1024 * 1024);
const corsOrigin = process.env.MEMORY_CORS_ORIGIN ?? "http://127.0.0.1:5173";

const primaryNavigation = [
  ["/chat", "Chat"],
  ["/ingest", "Capture"],
  ["/sessions", "Sessions"],
  ["/imports", "Import"],
  ["/memories", "Memory review"],
  ["/jobs", "Processing"],
  ["/metrics", "Metrics"],
  ["/status", "Status"],
  ["/", "Audit console"],
] as const;

function webPage(html: string, route?: string) {
  const missing = primaryNavigation.filter(([href]) => !html.includes(`href="${href}"`));
  // Local receipts and provenance links are an in-app continuation of the
  // current task. Keeping local links same-tab makes them work in embedded
  // browsers and preserves the user's back-navigation path. External
  // verification URLs and downloads may still intentionally open separately.
  let page = html.replace(/ target="_blank"(?=[^>]*href="\/[^"]*")/g, "");
  page = page.replace(/(<header\b[^>]*>)([\s\S]*?)(<\/header>)/i, (_match, opening, content, closing) => `${opening}${String(content).replace(/<a\b[^>]*>[\s\S]*?<\/a>/gi, "")}${closing}`);
  const authStyles = `<style>.api-access{display:inline-flex;align-items:center;gap:6px;margin-left:auto}.api-access summary{color:var(--accent,#7dd3fc);cursor:pointer}.api-access-panel{display:flex;gap:6px;align-items:center;padding:8px;background:var(--panel,#0d1a2d);border:1px solid var(--line,#20324d);border-radius:8px;position:absolute;right:14px;top:52px;z-index:4;box-shadow:0 12px 30px #0008}.api-access-panel input{width:190px;max-width:55vw;background:#091424;color:var(--text,#e7effa);border:1px solid var(--line,#20324d);border-radius:6px;padding:7px}.api-access-panel button{padding:7px 9px}.api-access-status{font-size:11px;color:var(--muted,#8da0bb)}@media(max-width:700px){.api-access{margin-left:0}.api-access-panel{left:14px;right:14px;top:100px;flex-wrap:wrap}.api-access-panel input{flex:1;min-width:150px}}</style>`;
  const authControl = `<details class="api-access" id="apiAccess"><summary>API access</summary><div class="api-access-panel"><input id="apiAccessKey" type="password" autocomplete="off" placeholder="Bearer API key" aria-label="API access key"><button type="button" id="apiAccessSave">Use key</button><button type="button" id="apiAccessClear">Clear</button><span class="api-access-status" id="apiAccessStatus" role="status" aria-live="polite"></span></div></details><script>(()=>{const storageKey='personal-memory-api-key';const panel=document.getElementById('apiAccess'),input=document.getElementById('apiAccessKey'),status=document.getElementById('apiAccessStatus');if(!panel||!input||!status)return;const saved=sessionStorage.getItem(storageKey)||'';if(saved){input.value=saved;status.textContent='Key ready for this session.'}const nativeFetch=window.fetch.bind(window);window.fetch=(resource,options={})=>{const key=sessionStorage.getItem(storageKey);const headers=new Headers(options.headers||{});if(key)headers.set('authorization','Bearer '+key);return nativeFetch(resource,{...options,headers}).then(response=>{if(response.status===401||response.status===403){panel.open=true;status.textContent='API access is required for this operation.'}return response})};document.getElementById('apiAccessSave').onclick=()=>{const key=input.value.trim();if(!key){status.textContent='Enter an API key first.';input.focus();return}sessionStorage.setItem(storageKey,key);status.textContent='Key saved for this browser session.';panel.open=false;location.reload()};document.getElementById('apiAccessClear').onclick=()=>{sessionStorage.removeItem(storageKey);input.value='';status.textContent='Key cleared.';panel.open=false;location.reload()}})();</script>`;
  const authNavigation = `<script>(()=>{const input=document.getElementById('apiAccessKey');if(input)input.placeholder='API key';const nativeOpen=window.open.bind(window);const localApi=(value)=>{const url=new URL(String(value),location.href);return url.origin===location.origin&&url.pathname.startsWith('/v1/')};window.open=(value,target,features)=>{if(!localApi(value))return nativeOpen(value,target,features);fetch(String(value)).then(async response=>{if(!response.ok)throw new Error('Download failed ('+response.status+')');const blob=await response.blob();const href=URL.createObjectURL(blob);nativeOpen(href,target,features);setTimeout(()=>URL.revokeObjectURL(href),60000)}).catch(error=>{const status=document.getElementById('apiAccessStatus');if(status){status.textContent=error.message;document.getElementById('apiAccess')?.setAttribute('open','')}});return null};document.addEventListener('click',event=>{const target=event.target;if(!(target instanceof Element))return;const link=target.closest('a[href]');if(!link||!localApi(link.href))return;event.preventDefault();fetch(link.href).then(async response=>{if(!response.ok)throw new Error('Source lookup failed ('+response.status+')');const blob=await response.blob();const href=URL.createObjectURL(blob);location.href=href;setTimeout(()=>URL.revokeObjectURL(href),60000)}).catch(error=>{const status=document.getElementById('apiAccessStatus');if(status){status.textContent=error.message;document.getElementById('apiAccess')?.setAttribute('open','')}})},true)})();</script>`;
  page = page.replace("</head>", `${authStyles}</head>`).replace("</header>", `${authControl}</header>`);
  page = page.replace("</header>", `${authNavigation}</header>`);
  page = page.replace("const key=sessionStorage.getItem(storageKey);const headers=new Headers(options.headers||{});", "const key=sessionStorage.getItem(storageKey);const target=typeof resource==='string'?new URL(resource,location.href):new URL(resource.url,location.href);if(target.origin!==location.origin)return nativeFetch(resource,options);const headers=new Headers(options.headers||{});");
  if (route) {
    const links = primaryNavigation.map(([href, label]) => `<a href="${href}"${href === route ? ` aria-current="page"` : ""}>${label}</a>`).join("");
    const responsiveNavigationStyle = `<style>.global-nav{min-width:0}@media(max-width:700px){header{height:auto;min-height:62px;flex-wrap:wrap;align-content:center;row-gap:10px;padding:12px 14px}header [style*="margin-left:auto"]{margin-left:0!important}.global-nav{width:100%;margin-left:0!important;overflow-x:auto;white-space:nowrap;padding-bottom:3px}.global-nav a{display:inline-block}}</style>`;
    page = page.replace("</head>", `${responsiveNavigationStyle}</head>`).replace("</header>", `<nav class="global-nav" aria-label="Primary navigation" style="margin-left:auto;display:flex;gap:14px;align-items:center;flex-wrap:wrap">${links}</nav></header>`);
  }
  if (route === "/status") {
    const recovery = `<section class="card" aria-labelledby="recovery-title"><h2 id="recovery-title">Backup and restore</h2><p class="muted">Snapshot export and restore are administrative operations. Export through authenticated <code style="overflow-wrap:anywhere;word-break:break-word">GET /v1/admin/export</code>, validate offline with <code style="overflow-wrap:anywhere;word-break:break-word">RESTORE_DRY_RUN=1 bun run restore -- snapshot.json</code>, and restore only after review with <code style="overflow-wrap:anywhere;word-break:break-word">RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY</code>.</p><p class="muted">The web interface does not perform an implicit graph restore. Use a native Neo4j dump for disaster recovery and keep exported snapshots protected like the underlying memory.</p></section>`;
    page = page.replace("</main>", `${recovery}</main>`);
  }
  if (route === "/chat") {
    const busyState = `<script>(()=>{const log=document.getElementById('messages'),form=document.getElementById('form'),status=document.getElementById('status');if(!log||!form||!status)return;log.setAttribute('aria-busy','false');form.addEventListener('submit',()=>log.setAttribute('aria-busy','true'));new MutationObserver(()=>{if(!status.querySelector('.working'))log.setAttribute('aria-busy','false')}).observe(status,{subtree:true,childList:true,characterData:true})})();</script>`;
    page = page.replace("</body>", `${busyState}</body>`);
  }
  if (route === "/") {
    const traceSearch = `<script>(()=>{const name=document.getElementById('name'),refresh=document.getElementById('refresh'),runs=document.getElementById('runs');if(!name||!refresh||!runs)return;const trace=document.createElement('input');trace.id='traceId';trace.type='search';trace.placeholder='Trace ID (paste from chat receipt)';trace.setAttribute('aria-label','Filter audit trace ID');name.insertAdjacentElement('afterend',trace);try{const saved=JSON.parse(localStorage.getItem('personal-memory-audit-filter')||'null');if(saved?.traceId)trace.value=saved.traceId}catch{}const nativeFetch=window.fetch.bind(window);window.fetch=(resource,options={})=>{const url=new URL(typeof resource==='string'?resource:resource.url,location.href);if(url.pathname==='/v1/admin/audit/runs'&&trace.value.trim())url.searchParams.set('name',trace.value.trim());return nativeFetch(url.toString(),options)};trace.addEventListener('input',()=>refresh.click());document.getElementById('saveFilter')?.addEventListener('click',()=>{try{const saved=JSON.parse(localStorage.getItem('personal-memory-audit-filter')||'{}');saved.traceId=trace.value.trim();localStorage.setItem('personal-memory-audit-filter',JSON.stringify(saved))}catch{}});document.getElementById('clearFilter')?.addEventListener('click',()=>{trace.value=''})})();</script>`;
    page = page.replace("</body>", `${traceSearch}</body>`);
    page = page.replace("</body>", `<script>document.querySelectorAll('#traceId').forEach((item,index)=>{if(index>0)item.remove()})</script></body>`);
    const traceActions = `<script>(()=>{const detail=document.getElementById('detail');if(!detail)return;const enhance=()=>{const stats=detail.querySelector('.trace-head .stats'),meta=detail.querySelector('.trace-head .meta');if(!stats||!meta||document.getElementById('copyTrace'))return;const id=(meta.textContent||'').replace(/^Trace\\s+/,'').trim();if(!id)return;const button=document.createElement('button');button.id='copyTrace';button.type='button';button.textContent='Copy trace ID';button.setAttribute('aria-label','Copy trace ID');button.onclick=async()=>{try{await navigator.clipboard.writeText(id);button.textContent='Trace ID copied';setTimeout(()=>{button.textContent='Copy trace ID'},1800)}catch{const status=document.getElementById('traceStatus');if(status)status.textContent='Clipboard unavailable; select the trace ID above to copy it.'}};stats.insertBefore(button,stats.firstChild)};new MutationObserver(enhance).observe(detail,{childList:true,subtree:true});enhance()})();</script>`;
    page = page.replace("</body>", `${traceActions}</body>`);
  }
  if (route === "/jobs") {
    const approvalGuard = `<script>(()=>{const status=document.getElementById('actionStatus');if(!status)return;document.addEventListener('click',event=>{const target=event.target;if(!(target instanceof Element))return;const button=target.closest('.approve');if(!button||button.dataset.confirmed==='1')return;event.preventDefault();event.stopImmediatePropagation();const previous=document.activeElement;status.innerHTML='Approve these extracted candidates? <button id="confirmApproval">Confirm approval</button><button id="cancelApproval">Cancel</button>';const close=()=>{status.textContent='';if(previous instanceof HTMLElement)previous.focus()};document.getElementById('cancelApproval').onclick=close;document.getElementById('confirmApproval').onclick=()=>{button.dataset.confirmed='1';close();button.click();delete button.dataset.confirmed};document.getElementById('confirmApproval').focus()},true)})();</script>`;
    page = page.replace("</body>", `${approvalGuard}</body>`);
  }
  if (route === "/metrics") {
    const trendPanel = `<script>(()=>{const main=document.querySelector('main');if(!main)return;const panel=document.createElement('section');panel.id='metricsTrend';panel.setAttribute('aria-live','polite');panel.innerHTML='<article style="background:var(--panel,#0d1a2d);border:1px solid var(--line,#20324d);border-radius:10px;padding:16px;margin-top:14px"><h2 style="font-size:14px;margin:0 0 12px">Recent trend</h2><div id="metricsAlerts" role="status" aria-live="polite"></div><div id="metricsTrendRows"></div></article>';main.append(panel);const safe=value=>String(value??'').replace(/[&<>"]/g,character=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[character]));const render=async()=>{try{const response=await fetch('/v1/admin/metrics');const data=await response.json();if(!response.ok)throw new Error(data.error||'Metrics unavailable');const alerts=data.alerts||[];document.getElementById('metricsAlerts').innerHTML=alerts.length?alerts.map(alert=>'<div style="color:#fb7185;margin-bottom:6px">Alert: '+safe(alert.message)+'</div>').join(''):'<div style="color:#a7e86e;margin-bottom:8px">No active thresholds.</div>';const trend=(data.trend||[]).slice(-12);const maximum=Math.max(1,...trend.map(item=>Number(item.retrievals)||0));document.getElementById('metricsTrendRows').innerHTML=trend.length?trend.map(item=>{const width=Math.max(2,Math.round((Number(item.retrievals)||0)/maximum*100));return '<div style="display:grid;grid-template-columns:135px 1fr 90px;gap:8px;align-items:center;border-top:1px solid var(--line,#20324d);padding:7px 0;font-size:12px"><time style="color:var(--muted,#8da0bb)">'+safe(new Date(item.minute).toLocaleTimeString())+'</time><div title="Retrievals '+safe(item.retrievals)+'" style="height:10px;background:linear-gradient(90deg,#7dd3fc '+width+'%,#172b45 '+width+'%)"></div><span style="color:var(--muted,#8da0bb)">'+safe(item.retrievals)+' req · '+safe(Number(item.meanLatencyMs||0).toFixed(1))+' ms</span></div>'}).join(''):'<div style="color:var(--muted,#8da0bb)">No trend samples yet.</div>'}catch(error){document.getElementById('metricsAlerts').textContent=error.message}};render();setInterval(render,10000)})();</script>`;
    page = page.replace("</body>", `${trendPanel}</body>`);
  }
  if (route === "/metrics") {
    const costSummary = `<script>(()=>{const article=document.querySelector('#metricsTrend article');if(!article)return;const cost=document.createElement('div');cost.id='metricsCost';cost.style.cssText='color:var(--muted,#8da0bb);font-size:12px;margin-bottom:10px';article.querySelector('h2')?.after(cost);const render=async()=>{try{const response=await fetch('/v1/admin/metrics');const data=await response.json();if(!response.ok)throw new Error(data.error||'Metrics unavailable');cost.textContent='Estimated retrieval cost this process: $'+Number(data.retrievals?.estimatedCostUsd||0).toFixed(6)}catch(error){cost.textContent=error.message}};render();setInterval(render,10000)})();</script>`;
    page = page.replace("</body>", `${costSummary}</body>`);
  }
  return page;
}

if (process.env.NODE_ENV === "production" && !process.env.MEMORY_API_KEY) {
  throw new Error("MEMORY_API_KEY is required when NODE_ENV=production");
}

function response(data: unknown, status = 200) {
  return Response.json(data, { status, headers: { "Access-Control-Allow-Origin": corsOrigin, "Access-Control-Allow-Headers": "authorization, content-type", "Access-Control-Allow-Methods": "GET, POST, OPTIONS" } });
}

function ingestionErrorStatus(error: unknown) {
  return error instanceof ContentTooLargeError ? 413 : 500;
}

function authorized(request: Request) {
  const apiKey = process.env.MEMORY_API_KEY;
  const adminKey = process.env.MEMORY_ADMIN_KEY;
  const provided = request.headers.get("authorization");
  return (!apiKey && !adminKey) || provided === `Bearer ${apiKey}` || provided === `Bearer ${adminKey}`;
}

function adminAuthorized(request: Request) {
  const expected = process.env.MEMORY_ADMIN_KEY ?? process.env.MEMORY_API_KEY;
  return process.env.NODE_ENV !== "production" && !expected
    ? true
    : Boolean(expected && request.headers.get("authorization") === `Bearer ${expected}`);
}

function adminAuthorizationFailure(request: Request) {
  const url = new URL(request.url);
  const run = audit.start({ name: "security.admin_authorization", kind: "security", input: { method: request.method, path: url.pathname }, metadata: { reason: "admin authorization required" }, tags: ["security", "auth", "admin"] });
  audit.fail(run, "admin authorization required");
  return response({ error: "admin authorization required", traceId: run.traceId }, 403);
}

type RecallInput = { query?: string; userId?: string; projectId?: string; sessionId?: string; limit?: number; includeHistorical?: boolean; includeTestData?: boolean; retrievalMethods?: RetrievalMethod[] };

function redactedRecallInput(body: RecallInput, queryChars = body.query?.length ?? 0) {
  return { queryChars, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId, limit: body.limit, includeHistorical: body.includeHistorical, includeTestData: body.includeTestData, retrievalMethods: body.retrievalMethods };
}

async function retrieve(body: RecallInput, auditParent?: { traceId: string; parentRunId: string }) {
  const started = performance.now();
  const trace = audit.start({ name: "memory.recall", kind: "retrieval", traceId: auditParent?.traceId, parentRunId: auditParent?.parentRunId, input: redactedRecallInput(body), metadata: { component: "personal-memory" }, tags: ["memory", "retrieval"] });
  let ok = false;
  let route: RetrievalRoute = "semantic";
  try {
    if (!body.query?.trim()) throw new Error("query is required");
    if (body.limit !== undefined && (!Number.isInteger(body.limit) || body.limit < 1 || body.limit > 50)) throw new Error("limit must be an integer between 1 and 50");
    if (body.retrievalMethods !== undefined && (!Array.isArray(body.retrievalMethods) || body.retrievalMethods.length < 1 || body.retrievalMethods.some((method) => !["lexical", "vector", "graph"].includes(method)))) throw new Error("retrievalMethods must contain lexical, vector, or graph");
    route = routeQuery(body.query);
    const plan = retrievalPlan(body.query, route);
    const scope = { ...body, includeHistorical: body.includeHistorical ?? route === "temporal" };
    const limit = body.limit ?? 12;
    const initialRun = audit.start({ name: "neo4j.recall", kind: "tool", traceId: trace.traceId, parentRunId: trace.id, input: { queryChars: body.query.length, scope: redactedRecallInput(body), limit, route }, metadata: { retrievalMethods: body.retrievalMethods ?? ["lexical", "vector", "graph"] } });
    let results;
    try {
      results = await store.recall(body.query, scope, limit, route);
      audit.finish(initialRun, { count: results.length, ids: results.map((result) => (result as Record<string, unknown>).id).filter(Boolean) });
    } catch (error) {
      audit.fail(initialRun, error);
      throw error;
    }
    let correctionQuery: string | null = null;
    const initialEvidence = assessEvidence(results);
    if (initialEvidence.needsCorrection && (correctionQuery = correctiveQuery(body.query))) {
      const correctionRun = audit.start({ name: "neo4j.corrective_recall", kind: "tool", traceId: trace.traceId, parentRunId: trace.id, input: { queryChars: correctionQuery.length, scope: redactedRecallInput(body, correctionQuery.length), limit, route }, metadata: { reason: "weak initial evidence" } });
      let corrected;
      try {
        corrected = await store.recall(correctionQuery, scope, limit, route);
        audit.finish(correctionRun, { count: corrected.length, ids: corrected.map((result) => (result as Record<string, unknown>).id).filter(Boolean) });
      } catch (error) {
        audit.fail(correctionRun, error);
        throw error;
      }
      const merged = new Map<string, Record<string, unknown>>();
      for (const result of [...results, ...corrected] as Array<Record<string, unknown>>) {
        const key = `${result.type}:${result.id}`;
        const existing = merged.get(key);
        if (!existing || Number(result.score ?? 0) > Number(existing.score ?? 0)) merged.set(key, result);
      }
      results = [...merged.values()].slice(0, limit);
    }
    ok = true;
    return { traceId: trace.traceId, results, route, plan, corrected: Boolean(correctionQuery), correctionQuery, evidence: assessEvidence(results), ...shouldAbstain(results, body.query) };
  } catch (error) {
    if (error instanceof Error) (error as Error & { traceId?: string }).traceId = trace.traceId;
    throw error;
  } finally {
    if (ok) audit.finish(trace, undefined, { route, durationMs: performance.now() - started });
    else audit.fail(trace, "recall failed", { route, durationMs: performance.now() - started });
    recordRetrievalMetric(body, ok, started, route);
  }
}

function recordRetrievalMetric(body: RecallInput, ok: boolean, started: number, route: string) {
  metrics.recordRetrieval({
    route,
    methods: body.retrievalMethods ?? ["lexical", "vector", "graph"],
    durationMs: performance.now() - started,
    ok,
    estimatedCostUsd: Number(process.env.MEMORY_RECALL_COST_USD ?? 0),
  });
}

function recordCodexToolRuns(events: unknown[], traceId: string, parentRunId: string) {
  for (const event of events) {
    if (!event || typeof event !== "object") continue;
    const record = event as Record<string, unknown>;
    const method = String(record.method ?? "");
    const params = record.params && typeof record.params === "object" ? record.params as Record<string, unknown> : {};
    const item = params.item && typeof params.item === "object" ? params.item as Record<string, unknown> : {};
    const itemType = String(item.type ?? params.type ?? "");
    if (!/(tool|command|mcp|shell|function)/i.test(`${method} ${itemType}`)) continue;
    const run = audit.start({
      name: `codex.${itemType || method.replaceAll("/", ".")}`,
      kind: "tool",
      traceId,
      parentRunId,
      input: { method, itemType, parameterKeys: Object.keys(params).sort() },
      metadata: { provider: "openai-codex", event: method },
      tags: ["codex", "tool"],
    });
    audit.finish(run, { parameterKeys: Object.keys(params).sort(), itemType });
  }
}

const server = Bun.serve({
  hostname: host,
  port,
  async fetch(request) {
    if (request.method === "OPTIONS") return new Response(null, { status: 204, headers: { "Access-Control-Allow-Origin": corsOrigin, "Access-Control-Allow-Headers": "authorization, content-type", "Access-Control-Allow-Methods": "GET, POST, OPTIONS" } });
    const requestUrl = new URL(request.url);
    const publicPage = request.method === "GET" && ["/", "/chat", "/ingest", "/sessions", "/imports", "/memories", "/jobs", "/status", "/metrics"].includes(requestUrl.pathname);
    if (!publicPage && !authorized(request)) {
      const securityRun = audit.start({ name: "security.unauthorized_request", kind: "security", input: { method: request.method, path: requestUrl.pathname }, metadata: { reason: "invalid bearer credential" }, tags: ["security", "auth"] });
      audit.fail(securityRun, "unauthorized");
      return response({ error: "unauthorized", traceId: securityRun.traceId }, 401);
    }
    const contentLength = Number(request.headers.get("content-length") ?? 0);
    if (contentLength > maxBodyBytes) {
      const url = new URL(request.url);
      const securityRun = audit.start({ name: "security.request_too_large", kind: "security", input: { method: request.method, path: url.pathname, contentLength }, metadata: { maxBodyBytes }, tags: ["security", "limits"] });
      audit.fail(securityRun, "request body too large");
      return response({ error: "request body too large", traceId: securityRun.traceId }, 413);
    }
    const url = requestUrl;
    try {
      if (url.pathname === "/" && request.method === "GET") return new Response(webPage(auditConsoleHtml(), "/"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/chat" && request.method === "GET") return new Response(webPage(chatHtml(), "/chat"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/ingest" && request.method === "GET") return new Response(webPage(ingestHtml(), "/ingest"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/sessions" && request.method === "GET") return new Response(webPage(sessionHtml(), "/sessions"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/imports" && request.method === "GET") return new Response(webPage(importHtml(maxBodyBytes), "/imports"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/memories" && request.method === "GET") return new Response(webPage(memoryLibraryHtml(), "/memories"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/jobs" && request.method === "GET") return new Response(webPage(jobsHtml(), "/jobs"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/status" && request.method === "GET") return new Response(webPage(statusHtml(), "/status"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/metrics" && request.method === "GET") return new Response(webPage(metricsHtml(), "/metrics"), { headers: { "content-type": "text/html; charset=utf-8", "Access-Control-Allow-Origin": corsOrigin } });
      if (url.pathname === "/health" && request.method === "GET") {
        await store.verify();
        return response({ ok: true, service: "personal-memory", neo4j: "connected" });
      }
      if (url.pathname === "/v1/admin/export" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response(await store.exportSnapshot());
      }
      if (url.pathname === "/v1/admin/stats" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response(await store.stats());
      }
      if (url.pathname === "/v1/admin/schema" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response(await store.schemaStatus());
      }
      if (url.pathname === "/v1/admin/metrics" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response(metrics.snapshot());
      }
      if (url.pathname === "/v1/admin/audit/runs" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const query = url.searchParams;
        return response({ runs: audit.query({ traceId: query.get("traceId") ?? undefined, status: query.get("status") as "running" | "success" | "error" | undefined, kind: query.get("kind") as never, name: query.get("name") ?? undefined, limit: Number(query.get("limit") ?? 50) }) });
      }
      const auditTraceMatch = url.pathname.match(/^\/v1\/admin\/audit\/traces\/([^/]+)$/);
      if (auditTraceMatch && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response({ traceId: decodeURIComponent(auditTraceMatch[1]), runs: audit.trace(decodeURIComponent(auditTraceMatch[1])) });
      }
      if (auditTraceMatch && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const traceId = decodeURIComponent(auditTraceMatch[1]);
        const body = (await request.json().catch(() => ({}))) as { confirm?: string };
        if (body.confirm !== "replay") return response({ error: "confirm must equal replay" }, 400);
        const root = audit.trace(traceId).find((run) => !run.parentRunId);
        if (!root || root.name !== "memory.recall" || !root.input || typeof root.input !== "object") return response({ replayable: false, error: "only memory.recall traces can be safely replayed" }, 409);
        const input = root.input as RecallInput;
        if (!input.query) return response({ replayable: false, error: "trace input does not contain a query" }, 409);
        return response({ replayable: true, originalTraceId: traceId, replay: await retrieve(input) });
      }
      if (url.pathname === "/v1/admin/audit/export" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response({ format: "personal-memory-audit", version: 1, exportedAt: new Date().toISOString(), runs: audit.query({ limit: 200 }) });
      }
      if (url.pathname === "/v1/admin/audit/stats" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        return response(audit.stats());
      }
      if (url.pathname === "/v1/admin/audit/clear" && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const body = (await request.json().catch(() => ({}))) as { confirm?: string };
        if (body.confirm !== "clear-audit") return response({ error: "confirm must equal clear-audit" }, 400);
        audit.clear();
        return response({ cleared: true, ...audit.stats() });
      }
      if (url.pathname === "/v1/admin/context-hubs" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const limit = Number(new URL(request.url).searchParams.get("limit") ?? 20);
        return response(await store.contextHubs(limit));
      }
      if (url.pathname === "/v1/admin/providers" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const [consolidation, embedding, reranker, faithfulnessJudge] = await Promise.all([
          probeProvider(process.env.MODEL_BASE_URL),
          probeProvider(process.env.EMBEDDING_BASE_URL),
          probeProvider(process.env.RERANKER_BASE_URL),
          probeProvider(process.env.FAITHFULNESS_JUDGE_BASE_URL),
        ]);
        return response({
          neo4j: { configured: true, database: process.env.NEO4J_DATABASE ?? "neo4j" },
          consolidation: { ...consolidation, model: process.env.MODEL_NAME ?? "local-model" },
          embedding: { ...embedding, dimensions: Number(process.env.EMBEDDING_DIMENSIONS ?? 1024) },
          reranker,
          faithfulnessJudge: { ...faithfulnessJudge, requiredForStrictBenchmark: process.env.FAITHFULNESS_REQUIRE_JUDGE === "1" },
        });
      }
      if (url.pathname === "/v1/events" && request.method === "POST") {
        const parsed = (await request.json()) as MemoryEvent;
        const body = { ...parsed, idempotencyKey: parsed.idempotencyKey ?? request.headers.get("idempotency-key") ?? undefined };
        const run = audit.start({ name: "ingest.event", kind: "chain", input: { kind: body.kind, source: body.source, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["ingest", "event"] });
        if (!body.content?.trim() || !body.kind) { audit.fail(run, "kind and content are required"); return response({ error: "kind and content are required", traceId: run.traceId }, 400); }
        try { const saved = await store.appendEvent(body); audit.finish(run, { id: saved.id, idempotent: saved.idempotent }); return response({ ...saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "event ingestion failed", traceId: run.traceId }, ingestionErrorStatus(error)); }
      }
      const eventMatch = url.pathname.match(/^\/v1\/events\/([^/]+)$/);
      if (eventMatch && request.method === "GET") {
        const event = await store.getEvent(decodeURIComponent(eventMatch[1]), { userId: url.searchParams.get("userId") || undefined, projectId: url.searchParams.get("projectId") || undefined, sessionId: url.searchParams.get("sessionId") || undefined });
        return event ? response(event) : response({ error: "event not found" }, 404);
      }
      if (url.pathname === "/v1/chat/auth" && request.method === "GET") {
        if (process.env.CODEX_APP_SERVER_DISABLED === "1") return response({ connected: false, disabled: true });
        try { return response({ connected: true, account: await codex.account() }); }
        catch (error) { return response({ connected: false, error: error instanceof Error ? error.message : "Codex App Server unavailable" }, 503); }
      }
      if (url.pathname === "/v1/chat/auth/start" && request.method === "POST") {
        const trace = audit.start({ name: "auth.chatgpt_login", kind: "security", input: { action: "begin_device_login" }, tags: ["auth", "chatgpt"] });
        if (process.env.CODEX_APP_SERVER_DISABLED === "1") { audit.fail(trace, "Codex App Server integration is disabled"); return response({ error: "Codex App Server integration is disabled", traceId: trace.traceId }, 503); }
        try { const result = await codex.beginChatGPTLogin() as Record<string, unknown>; audit.finish(trace, { fields: Object.keys(result).sort() }); return response({ ...result, traceId: trace.traceId }); }
        catch (error) { audit.fail(trace, error); return response({ error: error instanceof Error ? error.message : "Codex login unavailable", traceId: trace.traceId }, 503); }
      }
      if (url.pathname === "/v1/chat" && request.method === "POST") {
        if (process.env.CODEX_APP_SERVER_DISABLED === "1") { const trace = audit.start({ name: "chat.turn", kind: "chain", input: { integration: "codex", disabled: true }, tags: ["chat", "codex", "error"] }); audit.fail(trace, "Codex App Server integration is disabled"); return response({ error: "Codex App Server integration is disabled", traceId: trace.traceId }, 503); }
        const body = (await request.json()) as { message?: string; threadId?: string; sessionId?: string; userId?: string; projectId?: string; memoryScope?: "session" | "project" | "global"; captureTurn?: boolean; model?: string };
        const sessionId = body.sessionId ?? "web-chat";
        const memoryScope = body.memoryScope ?? "session";
        const captureTurn = body.captureTurn !== false;
        const retrievalScope = { userId: body.userId, projectId: memoryScope === "project" ? body.projectId : undefined, sessionId: memoryScope === "session" ? sessionId : undefined };
        const trace = audit.start({ name: "chat.turn", kind: "chain", input: { messageChars: body.message?.length ?? 0, threadId: body.threadId, sessionId, memoryScope, captureTurn, projectId: retrievalScope.projectId }, metadata: { provider: "openai-codex", model: body.model ?? process.env.CODEX_MODEL ?? "default" }, tags: ["chat", "codex", "memory"] });
        if (!body.message?.trim()) { audit.fail(trace, "message is required"); return response({ error: "message is required", traceId: trace.traceId }, 400); }
        if (!["session", "project", "global"].includes(memoryScope)) { audit.fail(trace, "memoryScope must be session, project, or global"); return response({ error: "memoryScope must be session, project, or global", traceId: trace.traceId }, 400); }
        if (memoryScope === "project" && !body.projectId?.trim()) { audit.fail(trace, "projectId is required for project memory scope"); return response({ error: "projectId is required for project memory scope", traceId: trace.traceId }, 400); }
        try {
          const retrieval = await retrieve({ query: body.message, ...retrievalScope, limit: 12 }, { traceId: trace.traceId, parentRunId: trace.id });
          const context = buildContextPack(retrieval.results as unknown as Parameters<typeof buildContextPack>[0], 12_000);
          const modelRun = audit.start({ name: "codex.turn", kind: "llm", traceId: trace.traceId, parentRunId: trace.id, input: { messageChars: body.message.length, contextChars: context.context.length, evidenceCount: context.results.length }, metadata: { provider: "openai-codex", model: body.model ?? process.env.CODEX_MODEL ?? "default", citationLocked: context.citationLocked } });
          const prompt = [
            "You are the private assistant for Personal Memory. Use the retrieved memory only when it is relevant. Do not invent facts. Cite retrieved claims using the exact [type:id] markers. If the evidence is insufficient, say so.",
            "Retrieved memory:\n" + (context.context || "(no relevant memory found)"),
            "User message:\n" + body.message,
          ].join("\n\n");
          try {
            const result = await codex.chat(prompt, { threadId: body.threadId, model: body.model ?? process.env.CODEX_MODEL });
            recordCodexToolRuns(result.events, trace.traceId, modelRun.id);
            audit.finish(modelRun, { textChars: result.text.length }, { turnId: result.turnId, eventCount: result.events.length, usage: result.usage });
            const validationRun = audit.start({ name: "answers.validate", kind: "validation", traceId: trace.traceId, parentRunId: trace.id, input: { answerChars: result.text.length, evidenceCount: context.results.length } });
            const citation = context.results.length
              ? validateCitationLockedAnswer(result.text, context.results as unknown as Array<{ id?: unknown; type?: unknown; evidence?: Array<{ id?: unknown; content?: unknown }> }>)
              : { valid: true, reason: "No retrieved evidence was available; the response must not claim private-memory facts.", citations: [] };
            audit.finish(validationRun, citation, { citationLocked: context.citationLocked });
            const saved = captureTurn ? await store.appendEvents([
              { kind: "user_message", content: body.message, source: "web-chat", userId: body.userId, projectId: body.projectId, sessionId },
              { kind: "agent_message", content: result.text, source: "openai-codex", userId: body.userId, projectId: body.projectId, sessionId, metadata: { traceId: trace.traceId, threadId: result.threadId, turnId: result.turnId } },
            ]) : [];
            audit.finish(trace, { textChars: result.text.length, threadId: result.threadId, turnId: result.turnId }, { citations: context.results.map((item) => ({ type: item.type, id: item.id, sourceEventIds: item.sourceEventIds })), citationValidation: citation, usage: result.usage });
            return response({ ...result, traceId: trace.traceId, memory: context, citation, captureTurn, savedEventIds: saved.map((event) => event.id) });
          } catch (error) {
            audit.fail(modelRun, error);
            throw error;
}

        } catch (error) {
          audit.fail(trace, error);
          return response({ error: error instanceof Error ? error.message : "chat request failed", traceId: trace.traceId }, 502);
        }
      }
      if (url.pathname === "/v1/events/batch" && request.method === "POST") {
        const parsed = (await request.json()) as { events?: MemoryEvent[] } | MemoryEvent[];
        const events = Array.isArray(parsed) ? parsed : parsed.events;
        const run = audit.start({ name: "ingest.event_batch", kind: "chain", input: { count: Array.isArray(events) ? events.length : 0, kinds: Array.isArray(events) ? [...new Set(events.map((event) => event?.kind).filter(Boolean))] : [] }, tags: ["ingest", "batch"] });
        if (!Array.isArray(events) || events.length < 1 || events.length > 100) { audit.fail(run, "events must contain between 1 and 100 items"); return response({ error: "events must contain between 1 and 100 items", traceId: run.traceId }, 400); }
        for (const event of events) if (!event?.content?.trim() || !event.kind) { audit.fail(run, "every event requires kind and content"); return response({ error: "every event requires kind and content", traceId: run.traceId }, 400); }
        try { const saved = await store.appendEvents(events); audit.finish(run, { count: saved.length }); return response({ events: saved, count: saved.length, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "batch ingestion failed", traceId: run.traceId }, ingestionErrorStatus(error)); }
      }
      if (url.pathname === "/v1/conversations" && request.method === "POST") {
        const body = (await request.json()) as ConversationInput;
        const run = audit.start({ name: "ingest.conversation", kind: "chain", input: { sessionId: body.sessionId, count: Array.isArray(body.messages) ? body.messages.length : 0, userId: body.userId, projectId: body.projectId }, tags: ["ingest", "conversation"] });
        if (!body.sessionId?.trim() || !Array.isArray(body.messages) || body.messages.length < 1 || body.messages.length > 100) {
          audit.fail(run, "sessionId and 1–100 messages are required"); return response({ error: "sessionId and 1–100 messages are required", traceId: run.traceId }, 400);
        }
        const normalized: MemoryEvent[] = [];
        for (const message of body.messages) {
          if (!message?.content?.trim() || !message.kind) { audit.fail(run, "every message requires kind and content"); return response({ error: "every message requires kind and content", traceId: run.traceId }, 400); }
          normalized.push({
            ...message,
            source: message.source ?? "codex",
            userId: body.userId,
            projectId: body.projectId,
            sessionId: body.sessionId,
            metadata: { ...message.metadata, ingestion: "conversation", origin: "codex" },
          });
        }
        try { const saved = await store.appendEvents(normalized); audit.finish(run, { count: saved.length }); return response({ sessionId: body.sessionId, events: saved, count: saved.length, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "conversation ingestion failed", traceId: run.traceId }, ingestionErrorStatus(error)); }
      }
      const summaryMatch = url.pathname.match(/^\/v1\/sessions\/([^/]+)\/summary$/);
      if (summaryMatch && request.method === "POST") {
        const body = (await request.json()) as Omit<SessionSummaryInput, "sessionId">;
        const run = audit.start({ name: "ingest.session_summary", kind: "chain", input: { sessionId: summaryMatch[1], userId: body.userId, projectId: body.projectId }, tags: ["ingest", "summary"] });
        if (!body.content?.trim()) { audit.fail(run, "summary content is required"); return response({ error: "summary content is required", traceId: run.traceId }, 400); }
        try { const memory = await store.createMemory({ ...body, id: body.idempotencyKey, sessionId: decodeURIComponent(summaryMatch[1]), category: "summary", status: "active", confidence: body.confidence ?? 0.9 }); audit.finish(run, { id: memory.id, idempotent: Boolean(body.idempotencyKey) }); return response({ ...memory, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "summary ingestion failed", traceId: run.traceId }, 500); }
      }
      const reflectionMatch = url.pathname.match(/^\/v1\/sessions\/([^/]+)\/reflection$/);
      if (reflectionMatch && request.method === "POST") {
        const body = (await request.json()) as Omit<SessionReflectionInput, "sessionId">;
        const sessionId = decodeURIComponent(reflectionMatch[1]);
        const run = audit.start({ name: "ingest.session_reflection", kind: "chain", input: { sessionId, userId: body.userId, projectId: body.projectId }, tags: ["ingest", "reflection"] });
        if (!body.content?.trim()) { audit.fail(run, "reflection content is required"); return response({ error: "reflection content is required", traceId: run.traceId }, 400); }
        const memories = [
          { content: body.content, category: "reflection" as const },
          ...(body.lessons ?? []).filter(Boolean).map((content) => ({ content, category: "belief" as const })),
          ...(body.failures ?? []).filter(Boolean).map((content) => ({ content, category: "failure" as const })),
        ];
        try { const saved = []; for (const [index, memory] of memories.entries()) saved.push(await store.createMemory({ ...body, ...memory, id: body.idempotencyKey ? `${body.idempotencyKey}:${index}` : undefined, sessionId, status: "active", sourceEventIds: body.sourceEventIds })); audit.finish(run, { count: saved.length, idempotent: Boolean(body.idempotencyKey) }); return response({ sessionId, memories: saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "reflection ingestion failed", traceId: run.traceId }, 500); }
      }
      if (url.pathname === "/v1/memories" && request.method === "GET") {
        const query = url.searchParams;
        const limit = Number(query.get("limit") ?? 50);
        const status = query.get("status") ?? undefined;
        if (status && !["active", "superseded", "archived", "retracted"].includes(status)) return response({ error: "invalid memory status" }, 400);
        return response({ memories: await store.listMemories({ userId: query.get("userId") ?? undefined, projectId: query.get("projectId") ?? undefined, sessionId: query.get("sessionId") ?? undefined }, limit, status) });
      }
      if (url.pathname === "/v1/memories" && request.method === "POST") {
        const body = (await request.json()) as CuratedMemory;
        const run = audit.start({ name: body.supersedesMemoryId ? "memory.correct" : "memory.create", kind: "chain", input: { category: body.category, projectId: body.projectId, sessionId: body.sessionId, supersedesMemoryId: body.supersedesMemoryId }, tags: ["memory", body.supersedesMemoryId ? "correction" : "capture"] });
        if (!body.content?.trim()) { audit.fail(run, "content is required"); return response({ error: "content is required", traceId: run.traceId }, 400); }
        try {
          const memory = await store.createMemory(body);
          audit.finish(run, { id: memory.id, status: memory.status, supersedesMemoryId: body.supersedesMemoryId });
          return response({ ...memory, traceId: run.traceId }, 201);
        } catch (error) { audit.fail(run, error); throw error; }
      }
      const impactMatch = url.pathname.match(/^\/v1\/memories\/([^/]+)\/impact$/);
      if (impactMatch && request.method === "GET") {
        const impact = await store.memoryImpact(decodeURIComponent(impactMatch[1]));
        return impact ? response(impact) : response({ error: "memory not found" }, 404);
      }
      const retractMatch = url.pathname.match(/^\/v1\/memories\/([^/]+)\/retract$/);
      if (retractMatch && request.method === "POST") {
        const body = (await request.json().catch(() => ({}))) as { reason?: string };
        const run = audit.start({ name: "memory.retract", kind: "security", input: { memoryId: retractMatch[1], reasonChars: body.reason?.length ?? 0 }, tags: ["memory", "forgetting"] });
        try {
          const memory = await store.retractMemory(decodeURIComponent(retractMatch[1]), body.reason);
          if (!memory) { audit.fail(run, "memory not found"); return response({ error: "memory not found" }, 404); }
          audit.finish(run, { id: memory.id, status: memory.status });
          return response({ ...memory, traceId: run.traceId });
        } catch (error) { audit.fail(run, error); throw error; }
      }
      const utilityMatch = url.pathname.match(/^\/v1\/memories\/([^/]+)\/utility$/);
      if (utilityMatch && request.method === "POST") {
        const body = (await request.json()) as { useful?: boolean; feedback?: string; traceId?: string };
        const run = audit.start({ name: "memory.utility", kind: "validation", traceId: body.traceId, input: { memoryId: utilityMatch[1], useful: body.useful, feedbackChars: body.feedback?.length ?? 0 }, tags: ["memory", "feedback"] });
        if (typeof body.useful !== "boolean") { audit.fail(run, "useful must be boolean"); return response({ error: "useful must be boolean", traceId: run.traceId }, 400); }
        try {
          const utility = await store.recordMemoryUtility(decodeURIComponent(utilityMatch[1]), body.useful, body.feedback);
          if (!utility) { audit.fail(run, "memory not found"); return response({ error: "memory not found" }, 404); }
          audit.finish(run, { id: utility.id, useful: body.useful });
          return response({ ...utility, traceId: run.traceId });
        } catch (error) { audit.fail(run, error); throw error; }
      }
      const archiveMatch = url.pathname.match(/^\/v1\/memories\/([^/]+)\/archive$/);
      if (archiveMatch && request.method === "POST") {
        const body = (await request.json().catch(() => ({}))) as { reason?: string };
        const run = audit.start({ name: "memory.archive", kind: "security", input: { memoryId: archiveMatch[1], reasonChars: body.reason?.length ?? 0 }, tags: ["memory", "retention"] });
        try {
          const memory = await store.archiveMemory(decodeURIComponent(archiveMatch[1]), body.reason);
          if (!memory) { audit.fail(run, "memory not found"); return response({ error: "memory not found" }, 404); }
          audit.finish(run, { id: memory.id, status: memory.status });
          return response({ ...memory, traceId: run.traceId });
        } catch (error) { audit.fail(run, error); throw error; }
      }
      if (url.pathname === "/v1/entities" && request.method === "POST") {
        const body = (await request.json()) as Entity;
        const run = audit.start({ name: "ingest.entity", kind: "chain", input: { nameChars: body.name?.length ?? 0, type: body.type, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["ingest", "entity"] });
        if (!body.name?.trim() || !body.type) { audit.fail(run, "name and type are required"); return response({ error: "name and type are required", traceId: run.traceId }, 400); }
        try { const saved = await store.upsertEntity(body); audit.finish(run, { id: saved.id }); return response({ ...saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "entity ingestion failed", traceId: run.traceId }, 500); }
      }
      if (url.pathname === "/v1/documents" && request.method === "POST") {
        const body = (await request.json()) as DocumentInput;
        const run = audit.start({ name: "ingest.document", kind: "chain", input: { titleChars: body.title?.length ?? 0, sourceChars: body.source?.length ?? 0, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["ingest", "document"] });
        if (!body.title?.trim() || !body.source?.trim()) { audit.fail(run, "title and source are required"); return response({ error: "title and source are required", traceId: run.traceId }, 400); }
        try { const saved = await store.upsertDocument(body); audit.finish(run, { id: saved.id }); return response({ ...saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "document ingestion failed", traceId: run.traceId }, 500); }
      }
      if (url.pathname === "/v1/chunks" && request.method === "POST") {
        const body = (await request.json()) as ChunkInput;
        const run = audit.start({ name: "ingest.chunk", kind: "chain", input: { documentId: body.documentId, ordinal: body.ordinal, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["ingest", "chunk"] });
        if (!body.documentId || !body.content?.trim() || body.ordinal < 0) { audit.fail(run, "documentId, content, and ordinal are required"); return response({ error: "documentId, content, and ordinal are required", traceId: run.traceId }, 400); }
        try { const saved = await store.upsertChunk(body); audit.finish(run, { id: saved.id }); return response({ ...saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "chunk ingestion failed", traceId: run.traceId }, 500); }
      }
      if (url.pathname === "/v1/claims" && request.method === "POST") {
        const body = (await request.json()) as ClaimInput;
        const run = audit.start({ name: "ingest.claim", kind: "chain", input: { predicate: body.predicate, subjectEntityId: body.subjectEntityId, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["ingest", "claim"] });
        if (!body.statement?.trim() || !body.predicate?.trim() || !body.subjectEntityId) { audit.fail(run, "statement, predicate, and subjectEntityId are required"); return response({ error: "statement, predicate, and subjectEntityId are required", traceId: run.traceId }, 400); }
        try { const saved = await store.createClaim(body); audit.finish(run, { id: saved.id }); return response({ ...saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "claim ingestion failed", traceId: run.traceId }, 500); }
      }
      if (url.pathname === "/v1/procedures" && request.method === "POST") {
        const body = (await request.json()) as ProcedureInput;
        const run = audit.start({ name: "ingest.procedure", kind: "chain", input: { titleChars: body.title?.length ?? 0, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["ingest", "procedure"] });
        if (!body.title?.trim() || !body.goal?.trim() || !body.steps?.length) { audit.fail(run, "title, goal, and steps are required"); return response({ error: "title, goal, and steps are required", traceId: run.traceId }, 400); }
        try { const saved = await store.upsertProcedure(body); audit.finish(run, { id: saved.id }); return response({ ...saved, traceId: run.traceId }, 201); }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "procedure ingestion failed", traceId: run.traceId }, 500); }
      }
      if (url.pathname === "/v1/recall" && request.method === "POST") {
        const body = (await request.json()) as RecallInput;
        try { return response(await retrieve(body)); } catch (error) { return response({ error: error instanceof Error ? error.message : "invalid recall request", traceId: (error as { traceId?: string })?.traceId }, 400); }
      }
      if (url.pathname === "/v1/context" && request.method === "POST") {
        const body = (await request.json()) as RecallInput & { maxChars?: number };
        if (body.maxChars !== undefined && (!Number.isInteger(body.maxChars) || body.maxChars < 256 || body.maxChars > 100_000)) {
          const run = audit.start({ name: "memory.context", kind: "validation", input: { queryChars: body.query?.length ?? 0, maxChars: body.maxChars, userId: body.userId, projectId: body.projectId, sessionId: body.sessionId }, tags: ["memory", "context", "validation"] });
          audit.fail(run, "maxChars must be an integer between 256 and 100000");
          return response({ error: "maxChars must be an integer between 256 and 100000", traceId: run.traceId }, 400);
        }
        try {
          const retrieval = await retrieve(body);
          const pack = buildContextPack(retrieval.results as unknown as Parameters<typeof buildContextPack>[0], body.maxChars ?? 12_000);
          return response({ ...retrieval, ...pack });
        } catch (error) { return response({ error: error instanceof Error ? error.message : "invalid context request", traceId: (error as { traceId?: string })?.traceId }, 400); }
      }
      if (url.pathname === "/v1/answers/validate" && request.method === "POST") {
        const body = (await request.json()) as {
          query?: string;
          answer?: string;
          results?: Array<{ id?: unknown; type?: unknown; content?: unknown; evidence?: Array<{ id?: unknown; content?: unknown }> }>;
          requireJudge?: boolean;
        };
        const run = audit.start({ name: "answers.validate", kind: "validation", input: { queryChars: body.query?.length ?? 0, answerChars: body.answer?.length ?? 0, resultCount: Array.isArray(body.results) ? body.results.length : 0, requireJudge: body.requireJudge === true }, tags: ["answers", "validation"] });
        if (!body.answer?.trim() || !Array.isArray(body.results)) { audit.fail(run, "answer and results are required"); return response({ error: "answer and results are required", traceId: run.traceId }, 400); }
        const citation = validateCitationLockedAnswer(body.answer, body.results);
        if (!citation.valid) {
          metrics.recordAnswerValidation(false);
          audit.fail(run, "citation validation failed", { citationCount: citation.citations.length });
          return response({ valid: false, citation, traceId: run.traceId }, 422);
        }
        const evidence = body.results.flatMap((result) => [
          typeof result.content === "string" ? result.content : "",
          ...(result.evidence ?? []).map((item) => typeof item.content === "string" ? item.content : ""),
        ]).filter(Boolean);
        const judge = new FaithfulnessJudge();
        let external: Awaited<ReturnType<FaithfulnessJudge["judge"]>> = null;
        try { external = body.query && judge.enabled ? await judge.judge({ query: body.query, answer: body.answer, evidence }) : null; }
        catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "faithfulness validation failed", traceId: run.traceId }, 502); }
        if (body.requireJudge && !external) {
          metrics.recordAnswerValidation(false);
          audit.fail(run, "external faithfulness judge is required");
          return response({ valid: false, citation, faithfulness: { judged: false, reason: "An external faithfulness judge is required." }, traceId: run.traceId }, 422);
        }
        if (external && !external.faithful) {
          metrics.recordAnswerValidation(false);
          audit.fail(run, "faithfulness validation failed", { judged: true });
          return response({ valid: false, citation, faithfulness: external, traceId: run.traceId }, 422);
        }
        metrics.recordAnswerValidation(true);
        audit.finish(run, { valid: true, citationCount: citation.citations.length, judged: Boolean(external) });
        return response({ valid: true, citation, faithfulness: external ?? { judged: false, fallback: "citation-only" }, traceId: run.traceId });
      }
      if (url.pathname === "/v1/jobs/next" && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const body = (await request.json().catch(() => ({}))) as { targetId?: string };
        return response({ job: await store.claimNextJob(body.targetId, [], process.env.CONSOLIDATION_REQUIRE_APPROVAL === "1") });
      }
      if (url.pathname === "/v1/jobs" && request.method === "GET") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const status = url.searchParams.get("status") ?? undefined;
        if (status && !["queued", "running", "completed", "failed"].includes(status)) return response({ error: "invalid job status" }, 400);
        return response({ jobs: await store.listJobs(status, Number(url.searchParams.get("limit") ?? 100)) });
      }
      const previewJobMatch = url.pathname.match(/^\/v1\/jobs\/([^/]+)\/preview$/);
      if (previewJobMatch && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const job = await store.getJobEvent(decodeURIComponent(previewJobMatch[1])) as { id: string; kind: string; event: MemoryEvent | null } | null;
        if (!job) return response({ error: "job not found" }, 404);
        if (job.kind !== "consolidate_event" || !job.event) return response({ error: "only event consolidation jobs can be previewed" }, 409);
        const run = audit.start({ name: "consolidation.preview", kind: "chain", input: { jobId: previewJobMatch[1], eventId: job.event.id }, tags: ["consolidation", "proposal"] });
        try {
          const proposal = await consolidator.extract(job.event as MemoryEvent);
          await store.saveJobProposal(decodeURIComponent(previewJobMatch[1]), proposal);
          audit.finish(run, { memoryCandidates: proposal.memories.length, entityCandidates: proposal.entities.length, claimCandidates: proposal.claims.length, procedureCandidates: proposal.procedures.length });
          return response({ jobId: job.id, proposal, traceId: run.traceId });
        } catch (error) { audit.fail(run, error); return response({ error: error instanceof Error ? error.message : "proposal preview failed", traceId: run.traceId }, 502); }
      }
      const approveJobMatch = url.pathname.match(/^\/v1\/jobs\/([^/]+)\/approve$/);
      if (approveJobMatch && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const run = audit.start({ name: "consolidation.approve", kind: "security", input: { jobId: approveJobMatch[1] }, tags: ["consolidation", "approval"] });
        const result = await store.approveJob(decodeURIComponent(approveJobMatch[1]));
        if (!result) { audit.fail(run, "job not found"); return response({ error: "job not found", traceId: run.traceId }, 404); }
        audit.finish(run, result);
        return response({ ...result, traceId: run.traceId });
      }
      const rejectJobMatch = url.pathname.match(/^\/v1\/jobs\/([^/]+)\/reject$/);
      if (rejectJobMatch && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const body = (await request.json().catch(() => ({}))) as { reason?: string };
        const run = audit.start({ name: "consolidation.reject", kind: "security", input: { jobId: rejectJobMatch[1], reasonChars: body.reason?.length ?? 0 }, tags: ["consolidation", "approval"] });
        const result = await store.rejectJob(decodeURIComponent(rejectJobMatch[1]), body.reason);
        if (!result) { audit.fail(run, "job not found"); return response({ error: "job not found", traceId: run.traceId }, 404); }
        audit.finish(run, result);
        return response({ ...result, traceId: run.traceId });
      }
      const jobMatch = url.pathname.match(/^\/v1\/jobs\/([^/]+)\/complete$/);
      if (jobMatch && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const body = (await request.json().catch(() => ({}))) as { error?: string };
        return response(await store.completeJob(jobMatch[1], body.error));
      }
      const retryMatch = url.pathname.match(/^\/v1\/jobs\/([^/]+)\/retry$/);
      if (retryMatch && request.method === "POST") {
        if (!adminAuthorized(request)) return adminAuthorizationFailure(request);
        const run = audit.start({ name: "consolidation.retry", kind: "security", input: { jobId: retryMatch[1] }, tags: ["consolidation", "retry"] });
        const job = await store.requeueFailedJob(retryMatch[1]);
        if (!job) { audit.fail(run, "failed job not found"); return response({ error: "failed job not found", traceId: run.traceId }, 404); }
        audit.finish(run, { jobId: job.id, status: job.status });
        return response({ ...job, traceId: run.traceId });
      }
      return response({ error: "not found" }, 404);
    } catch (error) {
      console.error(error);
      const message = error instanceof Error ? error.message : "internal error";
      if (message.startsWith("idempotency conflict")) return response({ error: message }, 409);
      return response({ error: message }, 500);
    }
  },
});

const shutdown = async () => {
  server.stop(true);
  await store.close();
  process.exit(0);
};
process.once("SIGINT", shutdown);
process.once("SIGTERM", shutdown);
console.log(`Personal Memory API listening on http://${server.hostname}:${server.port}`);
