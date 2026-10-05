# Validation Contract — Frontend Hardening

## Area: Frontend Hardening (Milestone 2)

**Scope**: Tailwind CSS migration, Vitest + testing-library setup, auth flow improvements, dashboard panels, navigation, log modal, error handling, loading states, responsive design, accessibility, and API client consolidation.

**Baseline**: Next.js 14.2 with App Router, React 18.3, TypeScript 5.8 (strict mode ON), vanilla CSS globals.css (1,637 lines), Playwright E2E only, 15 dashboard components, 7 route pages, useReducer-based state management, two separate API client modules.

---

## Build & Lint Toolchain

### VAL-FH-001: TypeScript strict-mode build succeeds
`npm run build` completes with zero errors under `"strict": true` in tsconfig.json. All pages (landing `/`, `/sign-in`, `/sign-up`, `/magic-code`, `/reset-password`, `/get-started`, `/janus` dashboard) compile without type errors. No `any` escape hatches remain unless explicitly justified with an inline comment explaining why.

Evidence: `npm run build` exit code 0; grep for `any` in source files returns only justified occurrences.

### VAL-FH-002: ESLint passes cleanly
`npm run lint` reports zero warnings and zero errors across all source files including new and refactored components, hooks, and library modules.

Evidence: `npm run lint` exit code 0 with clean output.

### VAL-FH-003: Vitest unit test suite passes
`npm run test` (Vitest) executes all unit tests and reports zero failures. Coverage includes at minimum: reducer logic (`dashboardReducer`), utility functions (`slugify`, `normalizeDate`, `parseDeploymentRuntimeEnvInput`, `parseWorkspaceViewFromLocation`, `parseWorkspaceViewParam`, `buildLogsEndpoint`, `getLogLineToneClass`, `lineMatchesSeverity`, `normalizeRuntimeEndpointForBrowser`, `containsUnauthorizedMessage`, `containsRuntimePermissionDeniedMessage`), and the API client error handling (`JanusApiError` construction, `toJanusApiError`, `isAbortError`).

Evidence: `npm run test` exit code 0; coverage report shows ≥80% line coverage for `reducer.ts`, `utils.ts`, and `api-client.ts`.

---

## Tailwind CSS Migration

### VAL-FH-004: Tailwind CSS replaces globals.css without layout regression
The 1,637-line monolithic `globals.css` is removed (or reduced to a minimal Tailwind `@layer base` reset) and replaced with Tailwind CSS utility classes. Every page renders with correct spacing, typography, colour, and alignment — no visible regressions compared to the original CSS-based rendering when viewed at 1280×800 desktop viewport. CSS custom properties (`:root` variables) are migrated to Tailwind theme configuration.

Evidence: Visual screenshot comparison at 1280×800 for all 7 route pages; `globals.css` file size reduced to ≤100 lines or removed entirely; Tailwind config contains theme values matching original `:root` variables (`--bg`, `--ink`, `--panel`, `--border`, `--action`, `--ring`, etc.).

### VAL-FH-005: Responsive design works at mobile and tablet breakpoints
All pages (landing, auth pages, dashboard) adapt correctly at 375px (mobile), 768px (tablet), and 1280px (desktop) widths. The sidebar collapses from a fixed 250px column to a full-width horizontal bar (or hamburger menu) on narrow screens. Forms remain usable; no horizontal overflow or overlapping elements appear. Quick action grid collapses to single column at 480px. Sidebar view buttons wrap to 2-column grid below 720px.

Evidence: Playwright screenshots at 375px, 768px, and 1280px viewports for `/`, `/sign-in`, `/janus`; no horizontal scrollbar detected via `document.documentElement.scrollWidth > document.documentElement.clientWidth`.

---

## Landing Page

### VAL-FH-006: Landing page renders and adapts to auth state
The landing page (`/`) loads, displays the hero section with kicker "Janus v0.1 Complete", capability cards (6 items: Source to Runtime, Temporary Domains, Operational Logs, Runner Reliability, Observability Ready, Self-Hosted Workflow), "How It Works" flow (5 steps), "Architecture Snapshot", "Trust and Operations", and footer. When the user is signed in, the primary CTA reads "Open Dashboard" and links to `/get-started`. When signed out, it reads "Sign Up & Start Building" and links to `/sign-up`. The session-checking chip reads "Checking session..." during the auth check and resolves to "Signed in: {email}" or disappears.

Evidence: Playwright test navigates to `/` both authenticated and unauthenticated; asserts CTA text, link href, chip content, and section headings.

---

## Authentication Flows

### VAL-FH-007: Sign-in flow completes successfully
Navigating to `/sign-in` shows the two-step form: entering a valid email enables "Continue", which reveals the password field. Submitting valid credentials polls the operation to "succeeded", exchanges the session, and redirects to `/get-started`. Invalid credentials display an inline error message (`.authError`). The "Forgot your password?" link navigates to `/reset-password?email={encoded-email}`. The "Email sign-in code" link navigates to `/magic-code?email={encoded-email}`. The "Don't have an account? Get started" link navigates to `/sign-up`.

Evidence: Playwright test completes sign-in with valid credentials and asserts redirect URL; test with invalid credentials asserts error message visibility.

### VAL-FH-008: Sign-up flow completes successfully
Navigating to `/sign-up` shows the two-step form: entering a valid email enables "Continue", which reveals the password field (minimum 10 characters enforced — submit button disabled below 10 chars). Submitting valid details creates the account via `authSignup`, polls operation to "succeeded", exchanges session, and redirects to `/get-started`. Errors display inline. The "Already have an account? Sign in" link navigates to `/sign-in`. The page heading displays "Create your account" (not "Sign in to Janus").

Evidence: Playwright test completes sign-up and asserts redirect; test asserts heading text; test enters 9-char password and confirms submit button is disabled.

### VAL-FH-009: Magic-code (passwordless) flow works end-to-end
Navigating to `/magic-code?email=user@example.com` pre-fills the email field, auto-sends a code request (via `authMagicCodeRequest`), and presents 6 individual digit inputs. Entering all 6 digits auto-submits verification (via `authMagicCodeVerify`). Successful verification exchanges session and redirects to `/get-started`. Invalid codes show "Invalid one-time code" error and reset all digit inputs, focusing the first field. The "Resend now" button re-sends the code. Arrow keys (Left/Right) navigate between OTP digit fields. Backspace on an empty field moves focus to the previous field.

Evidence: Playwright test with mocked API verifies auto-send, digit entry, auto-submit, error reset, resend button, and keyboard navigation.

### VAL-FH-010: Reset-password flow works
Navigating to `/reset-password?email=user@example.com` pre-fills the email field. Submitting the form calls `authResetPasswordRequest`, polls the operation, and on success shows "Reset instructions sent if the email is registered." flash message (`.authSuccess`). On failure, an error message displays. The "< Back to sign-in" link navigates to `/sign-in`. Submit button shows "Sending..." while in flight and is disabled.

Evidence: Playwright test submits reset form and asserts flash message; test with invalid email asserts error.

---

## Dashboard Navigation & Layout

### VAL-FH-011: Sidebar workspace view switching works
The sidebar renders 5 workspace view buttons (Pipeline, Projects, Auth, API, All) from `WORKSPACE_VIEWS` constant. Clicking each button dispatches `setWorkspaceView` and the dashboard shows only the relevant panels for that view. The currently active view button has the `workspaceViewActive` class (visually highlighted). Sidebar stats chips (Projects N, Builds N, Deployments N, Domains N) display actual data counts from state.

Evidence: Unit test for `dashboardReducer` verifying `setWorkspaceView` action; Playwright test clicks each view button and asserts panel visibility and active class.

### VAL-FH-012: Quick Actions navigate to correct panels
All 7 Quick Action buttons (Create Project → `new-project-panel`, Import Repo → `repo-import-panel`, Upload Bundle → `artifact-uploads-panel`, Review Builds → `builds-panel`, Create Deployment → `deployments-panel`, Open Endpoint → `domains-panel`, API Routes → `api-panel`) call `jumpToSection` which scrolls the viewport to the target section ID and switches the workspace view if needed. The target panel is visible and in the viewport after clicking.

Evidence: Playwright test clicks each Quick Action button and asserts `document.getElementById(sectionID)` is visible in viewport via `isIntersectingViewport()`.

### VAL-FH-013: Topbar displays correct contextual information
The topbar shows "Operations Dashboard" heading, the current workspace view label (e.g. "Current view: Pipeline"), the signed-in user's email chip (or "Signed out"), and a "Create a project in Projects view" chip when no projects exist. These values update reactively when state changes (view switch, sign-in, project creation).

Evidence: Playwright test asserts topbar content at initial load, after sign-in, and after view switch; unit test for Topbar component with various props.

---

## Dashboard Panels & CRUD Operations

### VAL-FH-014: Project creation and listing works
The "New Project" form (`ProjectCreatePanel`) accepts name, slug, description, repo provider (github), repo URL, and branch. Submitting creates a project via `JanusApiClient.createProject` and the project card appears in the Projects list (`ProjectsPanel`) with name, slug, status, ID, created date, and repo URL. The "Use Across Workflows" button on a project card dispatches `useProjectAcrossWorkflows`, propagating the project ID to repo import form, deployment form, and all project filters. Empty state ("No projects yet.") shows when the list is empty and loading is complete.

Evidence: Playwright test creates a project and asserts it appears in the list; test clicks "Use Across Workflows" and asserts form field values; test with no projects asserts empty state message.

### VAL-FH-015: Repository import triggers a build
The "Repo Import" form (`RepoImportPanel`) accepts project selection, provider, repo URL, and branch. Submitting calls `JanusApiClient.importRepo`; on success the import result card appears with message, build job ID, repository URL, and provider. The builds list refreshes to include the new build. The "Refresh Builds" button (also available within the panel) triggers a manual reload. The button shows "Importing..." while in flight and "Refreshing..." during builds reload.

Evidence: Playwright test submits import and asserts result card contents; asserts builds list contains new build ID.

### VAL-FH-016: File uploads work for source bundles and WASM artifacts
The "Direct Uploads" panel (`UploadsPanel`) contains two forms: source bundle upload (accepts `.zip`, `.tar.gz`, `.tgz` file via `<input type="file" accept=".zip,.tar.gz,.tgz,...">` with project selection, source ref, source kind dropdown) and prebuilt WASM artifact upload (accepts `.wasm` file via `<input type="file" accept=".wasm,application/wasm">` with project selection, source ref, optional runtime dropdown including auto-detect). Both forms submit multipart uploads via `JanusApiClient.uploadSourceBundle` / `JanusApiClient.uploadArtifact`; success messages appear (`.flash.success`). Error messages appear on failure (`.flash.error`). Submit buttons show "Uploading..." while in flight and are disabled without a project selected.

Evidence: Playwright test uploads a test `.zip` file and asserts success message; test uploads a test `.wasm` file and asserts success message; test submits without project and confirms button is disabled.

### VAL-FH-017: Builds panel displays status, filtering, and log viewing
The builds panel (`BuildsPanel`) shows build cards with ID, project ID, provider/ref, repo URL, created/updated dates, artifact key, artifact runtime, launch mode, build strategy, source kind, resource profile (CPU/RAM/Disk with confidence), and error snippet. The project filter dropdown narrows the list. Build stats chips (total, per-status counts like `status-built`, `status-failed`, `status-queued`) update with the filtered view. The "View Logs" button opens the log modal for the selected build using `buildLogsEndpoint`. The "Refresh Builds" button shows "Refreshing..." while loading. Permission hints display when `containsRuntimePermissionDeniedMessage` matches the error snippet. Empty state ("No builds yet.") shows when the list is empty.

Evidence: Playwright test asserts build card fields; test filters by project and asserts count change; test clicks "View Logs" and asserts log modal opens.

### VAL-FH-018: Deployment creation and listing works
The deployment form (`DeploymentsPanel`) accepts project, artifact build (filtered to "built" status builds via `deployableBuilds`), target type (preview/production dropdown), target ref, and runtime env (KEY=VALUE per line, parsed by `parseDeploymentRuntimeEnvInput` which validates key format `[A-Za-z_][A-Za-z0-9_]*`). Submitting calls `JanusApiClient.createDeployment`; on success the result card shows deployment ID, status, launch mode, domain binding, reachable endpoint link (with `normalizeRuntimeEndpointForBrowser` translation), and resource profile. The deployments list and domains list refresh. The project filter narrows the list. "View Logs" opens the log modal. Empty state ("No deployments yet.") shows when the list is empty.

Evidence: Playwright test creates deployment and asserts result card; test enters invalid env format and asserts error; test filters by project.

### VAL-FH-019: Domains panel displays bindings with endpoint links
The domains panel (`DomainsPanel`) lists domain records with domain name, project ID, deployment ID, revision, type, deployment status badge (`.status-{status}`), launch mode, and reachable endpoint link. External endpoint links open in a new tab (`target="_blank" rel="noreferrer"`). The project filter narrows the list. Domain stats chips update with filtered counts. The "Refresh Domains" button shows "Refreshing..." while loading. Empty state message ("No domains yet.") shows when no domains exist and loading is complete.

Evidence: Playwright test asserts domain card fields and endpoint link target attribute; test filters by project and asserts count.

---

## Log Modal

### VAL-FH-020: Log modal displays, filters, and exports logs
Clicking "View Logs" on a build or deployment opens a modal overlay (`logWindowBackdrop`) with a dialog (`role="dialog" aria-modal="true"`) containing log lines, build status, and last-update timestamp. Severity filter buttons (All, Errors, Warnings, Info, Success from `LOG_SEVERITY_FILTERS`) narrow visible lines using `lineMatchesSeverity` and update the "Visible: N" count chip. Additional chips show "Errors: N" and "Warnings: N". The "Auto update" checkbox toggles periodic log refresh (at `LOG_AUTO_REFRESH_MS` = 1s interval). "Copy" copies visible filtered logs to clipboard. "Download" saves logs as a file. "Refresh" manually fetches latest logs. "Close" button dismisses the modal. Clicking the backdrop also dismisses (via `onClick={props.onClose}` on backdrop). Error states display inline within the modal (`.flash.error`).

Evidence: Playwright test opens log modal, asserts dialog role, filters by severity, asserts visible count changes, copies to clipboard, downloads file, closes via button and via backdrop click.

---

## Error Handling & Loading States

### VAL-FH-021: Error boundaries catch and display errors gracefully
React error boundaries wrap major page sections (dashboard panels, auth forms) so that a component-level crash renders a user-friendly fallback message rather than a blank screen. Next.js `error.tsx` files exist at appropriate route segments (`/app/error.tsx`, `/app/janus/error.tsx`) to catch rendering errors. The fallback includes guidance to retry or navigate away.

Evidence: `error.tsx` files exist at route segments; unit test that forces a component throw verifies the error boundary renders the fallback UI.

### VAL-FH-022: Loading states show during data fetches
While data is loading, each panel shows its loading indicator: "Loading..." text for projects (`isProjectsLoading`), "Refreshing..." on refresh buttons (`isBuildsLoading`, `isDeploymentsLoading`, `isDomainsLoading`), "Importing..." on import submit (`isImporting`), "Uploading..." on upload submits (`isSubmittingSource`, `isSubmittingArtifact`), "Deploying..." on deployment submit (`isDeploying`), "Creating..." on project submit (`isProjectSubmitting`), "Loading logs..." in the log modal header (`isLoading`), "Checking session..." on landing page, "Signing in..." / "Creating account..." / "Sending..." on auth submit buttons. No panel shows stale empty states while its data is in flight (empty state guarded by `&& !isLoading` conditions).

Evidence: Unit test for each panel component rendered with `isLoading=true` asserts loading text is present and empty state is absent; Playwright test intercepts slow API responses and asserts loading indicators.

---

## Dark / Light Theme

### VAL-FH-023: Dark/light theme toggle switches colour scheme
A theme toggle control (`.themeToggleBtn`) is visible in the dashboard layout (topbar or sidebar). Clicking it toggles the `data-theme` attribute on the `<html>` element between absent (light) and `"dark"`. In dark mode, the CSS custom properties switch to dark palette values (`--bg: #0b1220`, `--ink: #e7eef8`, `--panel: #10223a`, `--border: #294769`, etc.). Theme preference persists across page reloads (via `localStorage` or cookie). All pages render correctly in both themes without contrast or readability issues.

Evidence: Playwright test clicks theme toggle, asserts `document.documentElement.dataset.theme === "dark"`, takes screenshot, reloads page, asserts theme persists; visual comparison of light vs dark at 1280×800.

---

## API Client Consolidation

### VAL-FH-024: API client modules consolidated into single module
The two separate API client modules (`app/lib/janus-api.ts` with functional style and `app/janus/lib/api-client.ts` with class-based `JanusApiClient`) are consolidated into a single module. All auth pages (`/sign-in`, `/sign-up`, `/magic-code`, `/reset-password`) and the dashboard (`/janus`) import from the same API client. No duplicate `JanusApiError` class definitions or duplicate `OperationEnvelope` type definitions exist across the codebase. The consolidated module supports both JSON requests and multipart uploads with consistent error handling (`toJanusApiError` envelope parsing) and request timeout management.

Evidence: Grep for `class JanusApiError` returns exactly one result; grep for `type OperationEnvelope` returns exactly one result; all imports of API functions resolve to a single module path; `app/lib/janus-api.ts` is either removed or re-exports from the canonical location.

---

## Accessibility

### VAL-FH-025: Escape key closes modal dialogs
Pressing the Escape key while the log modal is open dismisses it, equivalent to clicking the "Close" button or the backdrop. Focus returns to the element that triggered the modal (the "View Logs" button). This applies to any future modal dialogs as well.

Evidence: Playwright test opens log modal, presses Escape, asserts modal is closed and focus returns to the triggering button.

### VAL-FH-026: Focus management for modals and multi-step forms
When the log modal opens, focus moves to the first interactive element inside the modal (e.g., the "Close" button or the first severity filter). Focus is trapped within the modal while open — Tab/Shift+Tab cycle through modal controls without escaping to the page behind. In multi-step auth forms (sign-in, sign-up), advancing from the email step to the password step automatically focuses the password input. In the magic-code page, focus starts on the first OTP digit input after the email is pre-filled.

Evidence: Playwright test opens modal and asserts `document.activeElement` is inside the modal; test tabs through all modal controls and asserts focus stays within; test advances sign-in form and asserts password input is focused.

### VAL-FH-027: ARIA live regions announce dynamic state changes
Dynamic status changes (flash messages, error messages, loading state transitions, build/deployment status updates) are wrapped in or announced via ARIA live regions (`aria-live="polite"` or `role="status"`/`role="alert"`). Screen readers are notified when: an auth error appears, a flash success message appears, a loading indicator starts/completes, and log modal content updates.

Evidence: Grep for `aria-live` or `role="status"` or `role="alert"` in component files; unit test renders a component, triggers a state change, and asserts the live region contains the updated text.

### VAL-FH-028: Skip navigation link is available
A visually-hidden "Skip to main content" link is the first focusable element on every page. Activating it moves focus past the sidebar/topbar navigation to the main content area. The link becomes visible when focused.

Evidence: Playwright test tabs once on page load and asserts the skip link is focused and visible; activating it moves focus to `main` or `#main-content`.

---

## File Upload Interactions

### VAL-FH-029: File upload provides client-side validation feedback
Source bundle upload rejects files that do not match the accepted extensions (`.zip`, `.tar.gz`, `.tgz`) with a user-visible error before submission. WASM artifact upload rejects files not matching `.wasm`. File size limits (if configured) are enforced client-side with a descriptive error. After selecting a file, the file name is displayed to the user. After successful upload, the form resets (file input cleared, fields return to defaults).

Evidence: Playwright test selects an invalid file type and asserts error message appears without network request; test uploads valid file and asserts form reset (file input has no value, text fields reset).

---

## Dashboard Supporting Panels

### VAL-FH-030: Pipeline Guide tracks workflow progress
The Pipeline Guide panel (`PipelineGuide`) displays ordered steps toward achieving a live endpoint. Each step has a `done` boolean state. Steps that are complete show `pipelineStepDone` class styling (visual checkmark or strikethrough). Steps that are pending show `pipelineStepPending` class. The steps reflect actual dashboard state: e.g., "Create a project" becomes done when `projects.length > 0`, "Import a repository" becomes done when a build exists, etc.

Evidence: Playwright test with no data asserts all steps are pending; test after creating a project asserts first step is done; unit test for step derivation logic.

### VAL-FH-031: Context Summary reflects current selections
The Context Summary panel (`ContextSummary`) displays the currently selected project ID, build ID, deployment ID, and workspace view label. When no selection exists, each shows fallback text ("No project selected", "No build selected", "No deployment selected"). When "Use Across Workflows" is clicked on a project, the project ID updates in the context summary. The view label updates when switching workspace views.

Evidence: Playwright test asserts default "No project selected" text; test clicks "Use Across Workflows" and asserts project ID appears in context summary.

### VAL-FH-032: URL query parameter restores workspace view on load
Navigating to `/janus?view=projects` sets the initial workspace view to "projects" instead of the default "pipeline". The `parseWorkspaceViewFromLocation` utility correctly parses valid view names (`pipeline`, `projects`, `auth`, `reference`, `all`) case-insensitively and returns `null` for invalid values. Invalid or missing `?view=` parameters default to "pipeline".

Evidence: Unit test for `parseWorkspaceViewParam` with valid/invalid inputs; Playwright test navigates to `/janus?view=projects` and asserts Projects panels are visible.

---

## Error & Edge Case Handling

### VAL-FH-033: Unauthorized API responses show sign-in guidance
When any API call returns a 401 status or an error message containing "unauthorized", the error display includes the additional guidance text "Sign in first so the janus_access cookie is present." This applies to builds panel, deployments panel, and domains panel error states. The `containsUnauthorizedMessage` utility is used consistently across all panels.

Evidence: Unit test for `containsUnauthorizedMessage`; Playwright test with mocked 401 response asserts guidance text appears in the error flash.

### VAL-FH-034: Runtime permission denied errors show operator guidance
When a build or deployment error snippet contains "operation not permitted" with "chown" or "setgroups", the `runtimePermissionDeniedGuidance` utility generates an actionable hint directing the operator to allow CHOWN, SETUID, SETGID capabilities. This hint displays as a "Permission Hint" row on the affected build or deployment card.

Evidence: Unit test for `containsRuntimePermissionDeniedMessage` and `runtimePermissionDeniedGuidance` with matching and non-matching inputs; Playwright test with mocked error snippet asserts hint appears.

### VAL-FH-035: Auto-slug generation from project name
When typing a project name in the "New Project" form, the slug field auto-populates using the `slugify` utility (lowercase, replace non-alphanumeric with hyphens, collapse consecutive hyphens). Manual edits to the slug field override auto-generation. Slugify handles edge cases: leading/trailing spaces, special characters, consecutive spaces.

Evidence: Unit test for `slugify` with various inputs; Playwright test types project name and asserts slug field value matches expected slug.

---

## Sign-Up Page Correctness

### VAL-FH-036: Sign-up page heading displays correct text
The sign-up page (`/sign-up`) displays `<h1>` text appropriate for account creation (e.g., "Create your Janus account" or "Sign up for Janus"), not "Sign in to Janus". The current codebase incorrectly shows "Sign in to Janus" as the heading on the sign-up page — this must be corrected.

Evidence: Playwright test navigates to `/sign-up` and asserts `h1` text does not contain "Sign in"; visual inspection confirms heading matches the page purpose.

---

## Password Visibility Toggle

### VAL-FH-037: Password show/hide toggle works on auth forms
Both the sign-in and sign-up forms include a show/hide button (`.passwordEyeBtn`) adjacent to the password input. Clicking "Show" changes the input type from `password` to `text` and the button label to "Hide". Clicking "Hide" reverts to `password` type and "Show" label. The toggle does not clear or alter the password value.

Evidence: Playwright test on `/sign-in` types a password, clicks Show, asserts input type is "text" and value preserved, clicks Hide, asserts input type is "password".

---

## Auth Panel in Dashboard

### VAL-FH-038: Dashboard auth panel supports sign-in, sign-up, and sign-out
The Auth panel (`AuthPanel`) within the dashboard at `/janus` displays two modes: when signed out, toggle buttons switch between "Sign In" and "Sign Up" forms (with `modeActive` class on the active toggle). The sign-up form includes an additional "Name" field. When signed in, the panel shows "Signed in as {email}" with user name and a "Sign Out" button. Signing out clears the session and updates the panel to show the sign-in form. Auth errors and flash messages display below the form.

Evidence: Playwright test on `/janus` asserts auth panel toggle, fills sign-in form, signs in, asserts session display, clicks sign-out, asserts form reappears.

---

## Endpoint Normalization

### VAL-FH-039: Runtime endpoints normalize container-internal hostnames for browser
The `normalizeRuntimeEndpointForBrowser` utility rewrites hostnames `host.containers.internal`, `0.0.0.0`, and `::` to `localhost` in deployment and domain endpoint links. This ensures that links displayed in the browser are clickable and resolvable. Invalid or empty endpoints return empty string. Endpoint links in deployment result cards, deployment list cards, and domain cards all use this normalization.

Evidence: Unit test for `normalizeRuntimeEndpointForBrowser` with `http://host.containers.internal:8080/`, `http://0.0.0.0:3000/`, `http://[::]:8080/`, empty string, and malformed URL; grep confirms all endpoint `<a>` tags use normalized values.

---

## Operation Polling

### VAL-FH-040: Operation polling handles timeout and failure states
All operation-based flows (sign-in, sign-up, magic-code, reset-password, project creation, repo import, deployment creation) use the `pollOperation` function with configurable `intervalMs` (default 1s) and `timeoutMs` (default 60s). When polling times out, the user sees a clear "operation polling timed out" error message. When the operation fails or is dead-lettered, the failure message from `failure.message` is displayed. Polling does not continue after terminal states (`succeeded`, `failed`, `dead_lettered`, `expired`).

Evidence: Unit test for `pollOperation` with mocked responses testing succeeded, failed, dead_lettered, expired, and timeout scenarios; Playwright test with delayed API response asserts timeout error appears.

---

*Total assertions: 40 (VAL-FH-001 through VAL-FH-040)*
