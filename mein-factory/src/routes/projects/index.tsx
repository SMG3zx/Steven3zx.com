import { component$ } from "@builder.io/qwik";
import { WorkspacePage } from "../../components/workspace-page";
export default component$(() => <WorkspacePage eyebrow="PROJECTS" title="Projects" description="Repositories and workspaces connected to your local factory."><div class="empty-workspace"><div class="empty-icon">▣</div><h2>No projects connected yet</h2><p>Connect a local repository or create an isolated worktree to give the factory a codebase.</p><button class="primary-button">Connect project <span>→</span></button></div></WorkspacePage>);
