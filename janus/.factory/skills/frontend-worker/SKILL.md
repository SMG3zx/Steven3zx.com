---
name: frontend-worker
description: Next.js frontend implementation worker for Janus platform
---

# Frontend Worker

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the WORK PROCEDURE.

## When to Use This Skill

Use for features that involve Next.js frontend work:
- Component creation and decomposition
- Tailwind CSS styling
- State management (React Query, Zustand)
- Page routing and layouts
- API client changes
- Frontend unit tests (Vitest)
- TypeScript strict mode fixes

## Work Procedure

1. **Read context**: Read `.factory/library/architecture.md` and `.factory/library/environment.md` for conventions and environment details. Read the feature description carefully.

2. **Understand existing code**: Before writing anything, read the relevant existing files. Understand current component patterns, imports, naming conventions, and Tailwind classes in use. Match the existing style.

3. **Write tests first (TDD)**:
   - Create or update test files (`.test.tsx` or `.test.ts`) with failing tests.
   - Use Vitest + @testing-library/react for component tests.
   - Run `cd E:\Janus\frontend\web; npx vitest run --reporter=verbose path/to/test` to confirm tests fail (red).
   - If Vitest is not yet set up (milestone 3 feature), note this and write tests after setup.

4. **Implement**:
   - Use idiomatic Next.js App Router patterns:
     - Server Components by default (no `"use client"` unless needed for interactivity)
     - `"use client"` only for components with useState, useEffect, event handlers, or browser APIs
     - Layouts (`layout.tsx`) for shared UI, loading.tsx for suspense, error.tsx for error boundaries
   - Tailwind CSS for all styling (no inline styles, no CSS modules, no globals.css additions)
   - DRY: extract reusable components, custom hooks, and utility functions
   - TypeScript strict: explicit types, no `any`, proper interface definitions
   - Component composition: small, focused components with clear props interfaces

5. **Verify tests pass (green)**:
   - Run `cd E:\Janus\frontend\web; npx vitest run` to ensure ALL tests pass.
   - Fix any failures before proceeding.

6. **Manual verification**:
   - Start the dev server: `cd E:\Janus\frontend\web; npm run dev`
   - Use agent-browser or manually verify each UI change renders correctly.
   - Check responsive behavior at different viewport sizes.
   - Verify navigation between pages works.
   - Each verification = one `interactiveChecks` entry.
   - Stop the dev server after verification.

7. **Run validators**:
   - Run: `cd E:\Janus\frontend\web; npm run lint`
   - Run: `cd E:\Janus\frontend\web; npm run build`
   - All must pass (build includes TypeScript type checking).

8. **Update shared knowledge**: If you discover important patterns, gotchas, or environment details, update the relevant `.factory/library/` file.

## Example Handoff

```json
{
  "salientSummary": "Decomposed ProjectsPanel from monolithic dashboard into standalone component with proper TypeScript interfaces. Migrated all project-related styles from globals.css to Tailwind utilities. Added 5 Vitest tests covering render, empty state, loading, error, and create-project interaction.",
  "whatWasImplemented": "Extracted app/janus/components/ProjectsPanel.tsx into a self-contained component with ProjectsPanelProps interface accepting projects array, onSelect callback, and onCreate callback. Replaced 47 CSS class references with Tailwind equivalents. Created app/janus/components/__tests__/ProjectsPanel.test.tsx with 5 test cases. Updated app/janus/page.tsx to import and use the new component.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      { "command": "cd E:\\Janus\\frontend\\web; npx vitest run", "exitCode": 0, "observation": "5 tests pass in ProjectsPanel.test.tsx" },
      { "command": "cd E:\\Janus\\frontend\\web; npm run lint", "exitCode": 0, "observation": "No lint errors" },
      { "command": "cd E:\\Janus\\frontend\\web; npm run build", "exitCode": 0, "observation": "Build succeeds, 7 pages generated" }
    ],
    "interactiveChecks": [
      { "action": "Started dev server, navigated to /janus dashboard", "observed": "Projects panel renders with Tailwind styling, project cards display correctly with hover states" },
      { "action": "Clicked 'New Project' button in projects panel", "observed": "Create project form appears with proper input fields and validation" },
      { "action": "Tested responsive layout at 768px width", "observed": "Panel stacks vertically, cards adapt to single column" }
    ]
  },
  "tests": {
    "added": [
      { "file": "app/janus/components/__tests__/ProjectsPanel.test.tsx", "cases": [
        { "name": "renders project list", "verifies": "displays project names and descriptions from props" },
        { "name": "shows empty state", "verifies": "displays 'No projects' message when array is empty" },
        { "name": "handles create click", "verifies": "calls onCreate callback when new project button clicked" }
      ]}
    ]
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- API endpoint the component depends on doesn't exist or has changed
- Tailwind CSS is not yet configured (needed before any styling work)
- Vitest is not yet set up (needed before writing tests)
- Design requirements are ambiguous (layout, colors, spacing not specified)
- Component depends on state management library not yet installed
- Build or lint failures from pre-existing issues unrelated to this feature
