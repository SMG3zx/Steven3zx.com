# Monorepo Tools for a Solo Developer

Research date: 2026-10-04

## What “top 10” means here

There is no comparable global survey across JavaScript and polyglot monorepo tools. The closest recent usage ranking found is State of JavaScript 2025's **Monorepo Tools** question, answered by 10,251 respondents. It includes package managers, task runners, and an adjacent package testing tool, so this is a ranking of what that survey's respondents use, not a universal ranking of monorepo platforms.

Source: [State of JavaScript 2025 results](https://2025.stateofjs.com/en-US/other-tools/)

## Ten tools and useful features

| Survey order | Tool | Features that could help a solo developer |
|---|---|---|
| 1 | **pnpm** | Workspace linking, project filters, and shared dependency management. Useful when several JavaScript projects intentionally share a workspace. [Docs](https://pnpm.io/workspaces) |
| 2 | **npm Workspaces** | Built-in local package linking and commands scoped to one or more workspaces. A simple option when a repo is already organized as a related npm workspace. [Docs](https://docs.npmjs.com/cli/using-npm/workspaces/) |
| 3 | **Turborepo** | Runs tasks in dependency order, selects packages by name, path, or changed files, and caches declared outputs locally or remotely. [Docs](https://turborepo.dev/docs/crafting-your-repository/caching) |
| 4 | **Nx** | Builds a project/task graph, can infer tasks from project files, and supports affected-project selection and input-based caching. [Docs](https://nx.dev/docs/concepts/mental-model) |
| 5 | **Yarn Workspaces** | Workspace linking, focused installs, topological task execution, and constraints for enforcing consistent package rules. [Docs](https://yarnpkg.com/features/workspaces) |
| 6 | **Bun workspaces** | Workspace dependency installation and linking through `package.json`. Most useful if the projects already use Bun. [Docs](https://bun.sh/docs/pm/workspaces) |
| 7 | **Lerna** | Runs scripts across selected packages and supports change-scoped work; its task runner uses Nx for scheduling and caching. [Docs](https://lerna.js.org/docs/features/run-tasks) |
| 8 | **Yalc** | Tests a package in a consuming app using a local package copy. It is a specialized package-development helper, not a general monorepo task runner. [Docs](https://github.com/wclr/yalc) |
| 9 | **Rush** | Adds subset and incremental builds, dependency policies, and release workflows for large JavaScript package repos. Its management layer may be more than a solo developer needs. [Docs](https://rushjs.io/pages/intro/welcome/) |
| 10 | **Moon** | Provides a project/task graph, affected task selection, and task caching. Its project and task configuration can be applied across different toolchains. [Docs](https://moonrepo.dev/docs/how-it-works/task-graph) |

## Features that seem most useful for this repo

This repo contains independent projects with different languages and package managers. That makes a shared project catalog and consistent task entry points more immediately useful than forcing every project into one package-manager workspace.

1. **A project catalog:** identify each project's path, language/toolchain, and available commands.
2. **A single command to run a project task:** for example, build or check one named project without remembering its directory.
3. **Run only relevant projects:** use changed paths and explicit project dependencies to avoid building unrelated work.
4. **Parallel task execution:** run independent checks concurrently, while respecting known dependencies.
5. **Local caching, selectively:** cache deterministic tasks only after their inputs and outputs are clear. Remote caching is mainly useful if CI or another machine can reuse it.
6. **Keep project tooling independent:** do not consolidate lockfiles or package managers unless projects actually share dependencies.

Nx and Turborepo provide clear examples of graph-based task selection and caching, but their strongest out-of-the-box workflows are centered on JavaScript projects. Tools such as Bazel and Pants are designed for broader build modeling and language support, though they bring more configuration and upkeep.

Sources: [Monorepo tool feature comparison](https://monorepo.tools/compare), [Pants dependency inference](https://www.pantsbuild.org/dev/docs/introduction/how-does-pants-work)

Before adopting an affected-project workflow, verify how it detects changes. Many tools calculate changes using Git history; this repo uses Jujutsu, so the integration should be checked with the actual `jj` workflow.

## Recommendation

Start with the project catalog, one-command task runner, and project-level filtering. Add caching or a larger orchestrator if repeated builds become a real bottleneck.
