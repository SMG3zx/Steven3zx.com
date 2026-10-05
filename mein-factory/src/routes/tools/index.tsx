import { component$ } from "@builder.io/qwik";
import { WorkspacePage } from "../../components/workspace-page";
export default component$(() => <WorkspacePage eyebrow="WORKSPACE" title="Tools & MCP" description="Local capabilities available to agents, with permissions visible before execution."><div class="empty-workspace"><div class="empty-icon">⌘</div><h2>Tool registry is ready for wiring</h2><p>Filesystem, Git, test runner, and MCP tools will appear here as they are enabled.</p></div></WorkspacePage>);
