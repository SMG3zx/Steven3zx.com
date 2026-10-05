import { component$, $, useSignal } from "@builder.io/qwik";
import type { DocumentHead } from "@builder.io/qwik-city";

export default component$(() => {
  const prompt = useSignal("");
  const submittedPrompt = useSignal("");
  const agentResponse = useSignal("");
  const flowStatus = useSignal("");
  const submitTask = $(async () => {
    const value = prompt.value.trim();
    if (!value) return;
    submittedPrompt.value = value;
    prompt.value = "";
    agentResponse.value = "Starting the factory flow…";
    flowStatus.value = "Factory flow started";
    const apiUrl = import.meta.env.VITE_FACTORY_API_URL ?? "http://127.0.0.1:4111";
    try {
      const response = await fetch(`${apiUrl}/api/factory/runs`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ prompt: value, autoApprove: true }),
      });
      const data = (await response.json()) as { id?: string; status?: string; error?: string };
      if (!response.ok || !data.id) throw new Error(data.error ?? "factory flow could not start");
      flowStatus.value = data.status ?? "Factory flow started";
      for (let attempt = 0; attempt < 40; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 250));
        const runResponse = await fetch(`${apiUrl}/api/factory/runs/${data.id}`);
        const run = (await runResponse.json()) as { stage?: string; status?: string; plan?: string; error?: string };
        flowStatus.value = run.status ?? flowStatus.value;
        if (run.plan) agentResponse.value = run.plan;
        if (run.stage === "approval" || run.stage === "complete" || run.stage === "failed") break;
      }
    } catch {
      flowStatus.value = "Factory flow unavailable";
      agentResponse.value = "Task could not reach the local Mastra service.";
    }
  });

  return (
    <main class="app-shell">
      <aside class="sidebar">
        <div class="brand-mark"><span class="brand-glyph">M</span><span>MeinFactory</span></div>
        <div class="workspace-switcher"><span class="status-dot" /><span>Local workspace</span><span class="chevron">⌄</span></div>
        <nav class="nav-list" aria-label="Main navigation">
          <a class="nav-item active" href="#overview"><span>◈</span> Overview</a>
          <a class="nav-item" href="/factory/"><span>✦</span> Factory floor <span class="nav-count">3</span></a>
          <a class="nav-item" href="/projects/"><span>▣</span> Projects</a>
          <a class="nav-item" href="/runs/"><span>◷</span> Runs</a>
        </nav>
        <div class="sidebar-label">Workspace</div>
        <nav class="nav-list"><a class="nav-item" href="/agents/"><span>◎</span> Agents</a><a class="nav-item" href="/tools/"><span>⌘</span> Tools &amp; MCP</a><a class="nav-item" href="/settings/"><span>⚙</span> Settings</a></nav>
        <div class="sidebar-footer"><div class="model-chip"><span class="pulse-dot" /> Ollama connected</div><div class="model-name">qwen2.5:14b-instruct</div></div>
      </aside>
      <section class="content" id="overview">
        <header class="topbar"><div class="breadcrumb"><span>Workspace</span><b>/</b><strong>Overview</strong></div><div class="topbar-actions"><span class="local-badge">● Local only</span><button class="avatar" aria-label="Open profile">S</button></div></header>
        <div class="page-wrap">
          <section class="hero"><div><p class="eyebrow">SATURDAY, SEPTEMBER 19, 2026</p><h1>Build something <em>useful.</em></h1><p class="hero-copy">Your local software factory is ready. Describe an outcome and let your agents turn it into working software.</p></div><div class="hero-orbit" aria-hidden="true"><span class="orbit-core">✦</span><span class="orbit-ring ring-one" /><span class="orbit-ring ring-two" /></div></section>
          <section class="prompt-card" aria-label="Start a task"><div class="prompt-head"><span class="spark-icon">✦</span><span>Start a new build</span><span class="prompt-hint">⌘ ↵</span></div><textarea value={prompt.value} onInput$={(event) => (prompt.value = (event.target as HTMLTextAreaElement).value)} placeholder="What would you like to build?" onKeyDown$={(event) => { if ((event.metaKey || event.ctrlKey) && event.key === "Enter") submitTask(); }} /><div class="prompt-footer"><span class="context-note">Planner · Architect · Builder · Reviewer</span><button class="primary-button" onClick$={submitTask}>Create task <span>→</span></button></div>{submittedPrompt.value && <div class="submitted-note"><strong>{submittedPrompt.value}</strong>{flowStatus.value && <div class="flow-status"><span class="flow-pulse" />{flowStatus.value}</div>}{agentResponse.value && <p>{agentResponse.value}</p>}</div>}</section>
          <section class="section-heading"><div><p class="eyebrow">FACTORY FLOOR</p><h2>What is happening</h2></div><a href="#runs">View all runs <span>↗</span></a></section>
          <section class="stats-grid"><article class="stat-card"><div class="stat-icon blue">◌</div><div><span class="stat-label">Active runs</span><strong>03</strong><small><i class="up">+2</i> since yesterday</small></div></article><article class="stat-card"><div class="stat-icon green">✓</div><div><span class="stat-label">Completed this week</span><strong>18</strong><small><i class="up">+24%</i> from last week</small></div></article><article class="stat-card"><div class="stat-icon amber">◒</div><div><span class="stat-label">Awaiting approval</span><strong>02</strong><small>Needs your attention</small></div></article></section>
          <section class="lower-grid"><article class="panel activity-panel"><div class="panel-heading"><div><p class="eyebrow">LIVE ACTIVITY</p><h3>Recent runs</h3></div><span class="live-label"><i /> Live</span></div><div class="run-list"><div class="run-row"><span class="run-status running" /><div><strong>Build project dashboard</strong><small>Builder agent · 2m ago</small></div><span class="run-pill running-pill">Running</span></div><div class="run-row"><span class="run-status waiting" /><div><strong>Refactor auth middleware</strong><small>Waiting for your approval · 18m ago</small></div><span class="run-pill waiting-pill">Review</span></div><div class="run-row"><span class="run-status done" /><div><strong>Update API documentation</strong><small>Reviewer agent · 1h ago</small></div><span class="run-pill done-pill">Complete</span></div></div></article><article class="panel setup-panel"><div class="panel-heading"><div><p class="eyebrow">SYSTEM HEALTH</p><h3>Local services</h3></div><span class="health-label">All systems go</span></div><div class="service-list"><div><span class="service-indicator online" /><span>Qwik interface</span><code>:3000</code></div><div><span class="service-indicator online" /><span>Mastra runtime</span><code>:4111</code></div><div><span class="service-indicator online" /><span>Ollama</span><code>:11434</code></div><div><span class="service-indicator muted" /><span>Workspace sandbox</span><code>Ready</code></div></div><div class="panel-note">No external connections detected</div></article></section>
        </div>
      </section>
    </main>
  );
});

export const head: DocumentHead = {
  title: "MeinFactory — Local Software Factory",
  meta: [
    {
      name: "description",
      content: "A local-first software factory powered by Qwik, Mastra, Ollama, and Qwen.",
    },
  ],
};
