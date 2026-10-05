import { component$, Slot } from "@builder.io/qwik";

export const WorkspacePage = component$((props: { eyebrow: string; title: string; description: string }) => {
  return (
    <main class="app-shell">
      <aside class="sidebar">
        <div class="brand-mark"><span class="brand-glyph">M</span><span>MeinFactory</span></div>
        <div class="workspace-switcher"><span class="status-dot" /><span>Local workspace</span><span class="chevron">⌄</span></div>
        <nav class="nav-list" aria-label="Main navigation">
          <a class="nav-item" href="/"><span>◈</span> Overview</a>
          <a class="nav-item" href="/factory/"><span>✦</span> Factory floor <span class="nav-count">3</span></a>
          <a class="nav-item" href="/projects/"><span>▣</span> Projects</a>
          <a class="nav-item" href="/runs/"><span>◷</span> Runs</a>
        </nav>
        <div class="sidebar-label">Workspace</div>
        <nav class="nav-list"><a class="nav-item" href="/agents/"><span>◎</span> Agents</a><a class="nav-item" href="/tools/"><span>⌘</span> Tools &amp; MCP</a><a class="nav-item" href="/settings/"><span>⚙</span> Settings</a></nav>
        <div class="sidebar-footer"><div class="model-chip"><span class="pulse-dot" /> Ollama connected</div><div class="model-name">qwen2.5:14b-instruct</div></div>
      </aside>
      <section class="content">
        <header class="topbar"><div class="breadcrumb"><span>Workspace</span><b>/</b><strong>{props.title}</strong></div><div class="topbar-actions"><span class="local-badge">● Local only</span><button class="avatar" aria-label="Open profile">S</button></div></header>
        <div class="page-wrap workspace-page"><p class="eyebrow">{props.eyebrow}</p><h1>{props.title}</h1><p class="workspace-description">{props.description}</p><Slot /></div>
      </section>
    </main>
  );
});
