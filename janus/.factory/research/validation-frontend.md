# Validation Contract — Frontend Hardening

## Area: Frontend Hardening

### Build & Lint Toolchain

**VAL-FH-01 — TypeScript strict-mode build succeeds**
`npm run build` completes with zero errors under `"strict": true` in tsconfig.json. All pages (landing, sign-in, sign-up, magic-code, reset-password, get-started, janus dashboard) compile without type errors. No `any` escape hatches remain unless explicitly justified.

**VAL-FH-02 — ESLint passes cleanly**
`npm run lint` reports zero warnings and zero errors across all source files including new and refactored components, hooks, and library modules.

**VAL-FH-03 — Vitest unit test suite passes**
`npm run test` (Vitest) executes all unit tests and reports zero failures. Coverage includes at minimum: reducer logic, utility functions (slugify, normalizeDate, parseDeploymentRuntimeEnvInput, parseWorkspaceViewFromLocation), and the API client error handling.

### Tailwind CSS Migration

**VAL-FH-04 — Tailwind CSS replaces globals.css without layout regression**
The 27KB monolithic `globals.css` is removed and replaced with Tailwind CSS utility classes plus a minimal base layer. Every page renders with correct spacing, typography, colour, and alignment — no visible regressions compared to the original CSS-based rendering when viewed at 1280×800 desktop viewport.

**VAL-FH-05 — Responsive design works at mobile and tablet breakpoints**
All pages (landing, auth pages, dashboard) adapt correctly at 375px (mobile), 768px (tablet), and 1280px (desktop) widths. The sidebar collapses or becomes a hamburger menu on narrow screens. Forms remain usable; no horizontal overflow or overlapping elements appear.

### Landing Page

**VAL-FH-06 — Landing page renders and adapts to auth state**
The landing page (`/`) loads, displays the hero section, capability cards, "How It Works" flow, and footer. When the user is signed in, the primary CTA reads "Open Dashboard" and links to `/get-started`. When signed out, it reads "Sign Up & Start Building" and links to `/sign-up`. The session-checking chip appears during the auth check and resolves to "Signed in: {email}" or disappears.

### Authentication Flows

**VAL-FH-07 — Sign-in flow completes successfully**
Navigating to `/sign-in` shows the two-step form: entering a valid email enables "Continue", which reveals the password field. Submitting valid credentials redirects to `/get-started`. Invalid credentials display an inline error message. The "Forgot your password?" link navigates to `/reset-password?email=...`. The "Email sign-in code" link navigates to `/magic-code?email=...`.

**VAL-FH-08 — Sign-up flow completes successfully**
Navigating to `/sign-up` shows the two-step form: entering a valid email enables "Continue", which reveals the password field (minimum 10 characters enforced). Submitting valid details creates the account and redirects to `/get-started`. Errors display inline. The "Already have an account? Sign in" link navigates to `/sign-in`.

**VAL-FH-09 — Magic-code (passwordless) flow works end-to-end**
Navigating to `/magic-code?email=user@example.com` pre-fills the email, auto-sends a code request, and presents a 6-digit OTP input. Entering all 6 digits auto-submits verification. Successful verification redirects to `/get-started`. Invalid codes show an error and reset the digit inputs. The "Resend now" button re-sends the code. Arrow keys and backspace navigate between OTP digit fields correctly.

**VAL-FH-10 — Reset-password flow works**
Navigating to `/reset-password?email=user@example.com` pre-fills the email. Submitting the form shows a success flash ("Reset instructions sent if the email is registered.") or an error message. The "Back to sign-in" link navigates to `/sign-in`.

### Dashboard Navigation & Layout

**VAL-FH-11 — Sidebar workspace view switching works**
The sidebar renders workspace view buttons (Pipeline, Projects, Auth, Reference, All). Clicking each button filters the dashboard to show only the relevant panels. The currently active view is visually highlighted. Sidebar stats chips (Projects N, Builds N, Deployments N, Domains N) update to reflect actual data counts.

**VAL-FH-12 — Quick Actions navigate to correct panels**
All 7 Quick Action buttons (Create Project, Import Repo, Upload Bundle, Review Builds, Create Deployment, Open Endpoint, API Routes) scroll the viewport to the target section and switch the workspace view if needed. The target panel is visible and in the viewport after clicking.

**VAL-FH-13 — Topbar displays correct contextual information**
The topbar shows the current workspace view label, the signed-in user's email (or "Signed out"), and a project creation hint when no projects exist. These values update reactively when state changes.

### Dashboard Panels & CRUD Operations

**VAL-FH-14 — Project creation and listing works**
The "New Project" form accepts name, slug, description, repo provider, repo URL, and branch. Submitting creates a project via the API and the project appears in the Projects list. The "Use Across Workflows" button on a project card propagates the project ID to the repo import, uploads, and deployment forms. Empty state ("No projects yet.") shows when the list is empty.

**VAL-FH-15 — Repository import triggers a build**
The "Repo Import" form accepts project selection, provider, repo URL, and branch. Submitting creates an import operation; on success the import result card appears with message, build job ID, and repo URL. The builds list refreshes to include the new build. The "Refresh Builds" button triggers a manual reload.

**VAL-FH-16 — File uploads work for source bundles and WASM artifacts**
The "Direct Uploads" panel contains two forms: source bundle upload (zip/tar.gz/tgz file with project, source ref, source kind) and prebuilt WASM artifact upload (.wasm file with project, source ref, optional runtime). Both forms submit multipart uploads; success messages appear with build details. Error messages appear on failure. The forms reset after successful submission.

**VAL-FH-17 — Builds panel displays status, filtering, and log viewing**
The builds panel shows build cards with ID, project, provider/ref, repo URL, dates, artifact info, resource profile, and error snippets. The project filter dropdown narrows the list. Build stats chips (total, per-status counts) update with the filtered view. The "View Logs" button opens the log modal for the selected build.

**VAL-FH-18 — Deployment creation and listing works**
The deployment form accepts project, artifact build (filtered to "built" status), target type (preview/production), target ref, and runtime env (KEY=VALUE per line). Submitting creates a deployment; on success the result card shows deployment ID, status, domain, reachable endpoint link, and resource profile. The deployments list and domains list refresh. The project filter narrows the list. "View Logs" opens the log modal.

**VAL-FH-19 — Domains panel displays bindings with endpoint links**
The domains panel lists domain records with domain name, project, deployment ID, revision, type, deployment status badge, and reachable endpoint link. External endpoint links open in a new tab. The project filter narrows the list. Empty state message shows when no domains exist.

### Log Modal

**VAL-FH-20 — Log modal displays, filters, and exports logs**
Clicking "View Logs" on a build or deployment opens a modal overlay with log lines, build status, and last-update timestamp. Severity filter buttons (All, Errors, Warnings, Info, Success) narrow visible lines and update the "Visible" count chip. The "Auto update" checkbox toggles periodic log refresh. "Copy" copies visible logs to clipboard. "Download" saves logs as a file. "Close" (button or backdrop click) dismisses the modal. Error states display inline within the modal.

### Error Handling & Loading States

**VAL-FH-21 — Error boundaries catch and display errors gracefully**
React error boundaries wrap major page sections so that a component-level crash renders a user-friendly fallback message rather than a blank screen. The fallback includes guidance to retry or navigate away.

**VAL-FH-22 — Loading states show during data fetches**
While data is loading, each panel shows its loading indicator: "Loading..." text for projects, "Refreshing..." on refresh buttons, "Importing..." / "Uploading..." / "Deploying..." / "Creating..." on submit buttons, and "Loading logs..." in the log modal. No panel shows stale empty states while its data is in flight.
