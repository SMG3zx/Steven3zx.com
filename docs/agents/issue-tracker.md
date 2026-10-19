# Issue tracker: Linear

Issues and specs for this repo live in Linear, in the Engineering team (`ENG`) under the **Janus** project (`P-ENG-7`).

- Workspace: `https://linear.app/gateswaysolutions`
- Project: `https://linear.app/gateswaysolutions/project/janus-c69bbe7daede`

Older specs and tickets from before this switch are local markdown under `.scratch/`, for example `.scratch/job-lifecycle-and-account-deletion/`. They are history. Don't add new work there.

## Conventions

- One feature is one Linear project or one parent issue. The spec is the parent issue's description.
- Implementation work is one sub-issue per ticket, under its parent. Use Linear's native parent and sub-issue relations.
- Triage state is a label (see `triage-labels.md` for the role strings), not a file line.
- Comments and conversation history live in Linear comments on the issue.
- Blocking uses Linear's native blocked-by relation, so the frontier shows in the Linear UI.
- Statuses: `Backlog` for undecided work, `Todo` for ready work, `In Progress` while claimed, and `Done` or `Canceled` when closed.

## When a skill says "publish to the issue tracker"

Create a Linear issue in team `Engineering`, project `Janus`. Use a parent issue for a feature, and a sub-issue for each ticket. Apply the matching label from `triage-labels.md`.

## When a skill says "fetch the relevant ticket"

Read the Linear issue by its identifier, for example `ENG-44`, or by the URL the user gives.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a Linear issue labelled `wayfinder:map`, and each **child ticket** is a sub-issue of the map.

- **Map**: a `wayfinder:map` issue. Its description holds the Destination, Notes, Decisions so far, Not yet specified, and Out of scope sections.
- **Child ticket**: a sub-issue of the map, with the question in its description. A `wayfinder:<type>` label records the type: `wayfinder:research`, `wayfinder:prototype`, `wayfinder:grilling`, or `wayfinder:task`.
- **Blocking**: Linear's native blocked-by relation. A ticket is unblocked when every issue blocking it is `Done` or `Canceled`.
- **Frontier**: sub-issues of the map that are open, unblocked, and unassigned. Order them by identifier, and the first one wins.
- **Claim**: assign the ticket to the person driving the map, and set it to `In Progress`, before any work.
- **Resolve**: post the answer as a comment on the ticket, set the ticket to `Done`, and append a context pointer (gist and link) to the map's Decisions so far.

Existing wayfinder labels: `wayfinder:map`, `wayfinder:grilling`, `wayfinder:research`, and `wayfinder:prototype`. `wayfinder:task` is not created yet. Create it the first time a task ticket is needed.
