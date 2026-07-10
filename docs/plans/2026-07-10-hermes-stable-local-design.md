# Hermes Stable Local Design

## Goal

Turn the current AWS provisioning MVP into a dependable localhost tool before
the final real-AWS acceptance pass. The milestone hardens lifecycle behavior,
makes failures actionable, separates list and data-entry workflows, adds
blueprint editing/copying and complete job history, and makes local setup and
verification repeatable.

## Constraints

- Localhost and a single operator remain the deployment model.
- The default listener is `127.0.0.1:8080`, not all network interfaces.
- Existing local SQLite data is disposable; migrations still remain additive
  and testable so a normal restart is predictable.
- Real AWS acceptance is explicitly deferred to the final milestone.
- Keep Go templates, htmx, Tailwind CSS, SQLite, and Pulumi Automation API.
- Preserve the existing PRODUCT/DESIGN direction: a calm, compact operations
  console with system typography, restrained color, and explicit risky actions.
- List pages display and operate on data. Creating or editing data happens on a
  dedicated URL, not in an always-visible form embedded above the list.

## Information Architecture

The primary navigation remains Accounts, Projects, Blueprints, and
Environments. Navigation has a visible active state and remains available on
deep pages.

| Resource | List | Create / Edit | Other workflow |
| --- | --- | --- | --- |
| Account | `GET /accounts` | `GET /accounts/new`, `POST /accounts` | delete from list |
| Project | `GET /projects` | `GET /projects/new`, `POST /projects` | delete from list |
| Blueprint | `GET /blueprints` | `GET /blueprints/new`, `GET /blueprints/{id}/edit`, `POST /blueprints`, `POST /blueprints/{id}` | `GET /blueprints/{id}/duplicate`, deploy page, delete |
| Environment | `GET /environments` | created only through a blueprint deployment page | detail workspace and lifecycle actions |
| Job | linked history on the environment detail | none | `GET /jobs/{id}` for immutable diagnostics |

Dedicated pages are preferred to dialogs in this milestone. Account and
blueprint forms are too important or too long for a modal; using the same
pattern for projects keeps navigation predictable and gives every form a
stable URL, native back behavior, and accessible focus order. Destructive
actions remain in context and require confirmation.

The account form stores the operator's default AWS region alongside the
validated credentials. The value feeds catalog fallbacks and must not be
silently discarded. Secret Access Key input is never echoed after validation
failure and uses a password control with an explicit show/hide command.

## Lifecycle Contract

The orchestrator is the only authority that decides whether an asynchronous
action may be queued. UI visibility is guidance, not a security or correctness
boundary.

| Environment status | Allowed commands |
| --- | --- |
| `pending` | preview |
| `preview_ready` | up, destroy preview (discard the previewed environment) |
| `up` | refresh, destroy preview |
| `failed` | retry the latest failed action, or destroy preview for cleanup |
| `destroy_preview_ready` | destroy, cancel destroy preview |
| `previewing`, `provisioning`, `refreshing`, `destroy_previewing`, `destroying` | none |
| `destroyed` | none |

Unknown actions, missing environments, invalid transitions, a missing failed
job for retry, and concurrent jobs return typed errors. `Retry` is a dedicated
orchestrator operation: it reads the newest failed job and requeues exactly
that action. Direct handlers cannot invent a retry action.

Queueing is a store transaction, not a check followed by separate writes. The
transaction compares the current environment status, inserts the queued Job,
and moves the environment to its action-specific transient status. SQLite's
single writer plus conditional updates makes a destroy/cancel race deterministic:
only one side can commit. `destroy_previewing` is added so a queued destroy
preview is visible and cannot be cancelled before it has completed.

Destroy preview records the status it should return to when cancelled (`up`,
`preview_ready`, or `failed`). This permits safe cleanup of a partially-created
failed environment without lying that the environment is healthy when the
cleanup preview is cancelled. A small migration adds the resume status to
environments.

Starting a worker conditionally moves its Job from queued to running. Finishing
a worker stores Job terminal state, Environment terminal state, summary,
outputs, complete logs, and error in one transaction. A stale or invalid
transition fails rather than producing split-brain status. Panic, cancellation,
and shutdown finalization use a short context detached from the cancelled
worker context so the final error and log line survive restart. Startup orphan
recovery returns an error if reconciliation cannot be persisted.

Handlers never discard enqueue or persistence errors. They redirect back to
the environment workspace with a safe, user-facing error message. The page
renders it in an `aria-live` alert with a recovery action where one exists.
The worker rejects unknown persisted actions rather than marking them as
successful.

## Blueprint Workflows

One shared blueprint form model powers create, edit, and duplicate pages. It
contains the selected project/account, structured `BlueprintParams`, form
action, page title, submit label, and field errors. Parsing is centralized so
create and edit enforce identical defaults and validation.

- Create starts with low-cost defaults and an empty name.
- Edit loads the current blueprint and updates it in place. Existing
  environments remain correct because they already store a blueprint snapshot.
- Duplicate opens the create form prefilled from the source and with a distinct
  proposed name; saving creates a new record rather than mutating the source.
- Deploy opens a short confirmation page containing the blueprint summary and
  environment name. The list no longer contains an inline environment form.
- If the initial preview cannot be queued, deployment compensates by removing
  the just-created pending environment so the list never contains an orphan.
- Validation errors re-render the form with non-secret user input preserved and
  errors placed next to the relevant field.

Optional VPC, RDS, and Redis sections use progressive disclosure while keeping
their controls server-compatible. AWS catalog selectors retain their cached
fallback behavior and htmx updates. Edit/duplicate pages pass explicit selected
region, instance type, and AMI values through catalog refreshes so asynchronous
option replacement cannot reset the saved selection.

## Job History And Diagnostics

The environment workspace shows current state and outputs first, then a
chronological job table with action, status, timestamps, summary, and a detail
link. The history rows poll while work is active so their status does not become
stale. The newest active job owns the live log panel. Completed jobs use their
persisted logs and never open a stale SSE connection.

Polling uses lightweight Job summaries that exclude the `logs` column. Full
logs are read only for a selected Job detail or the active stream. Polling stops
when no Job is queued or running.

`GET /jobs/{id}` provides an immutable diagnostic page with:

- environment and back link;
- translated action/status plus raw identifiers where useful;
- queued, started, and finished timestamps;
- create/update/delete/same summary;
- a prominent failure message with a recovery path;
- complete, copyable logs in the existing dark operational log surface.

The SSE handler first loads the Job. Missing jobs return 404; finished jobs
replay persisted logs, emit `done`, and close immediately. This makes historical
logs reliable after a server restart and avoids attaching an idle broker topic.

## UI System

The existing steel-blue accent, cool neutral canvas, semantic status colors,
system font, and 6px control radius remain. Automated recommendations that
suggest marketing layouts or decorative font pairings are rejected because
they conflict with PRODUCT.md and DESIGN.md.

- Pages are unframed work surfaces, not floating cards.
- Every list page has one page title, a compact description, and one primary
  create action in the header.
- Tables optimize scanning on desktop and convert to labelled stacked rows on
  narrow screens; no text or action group may overflow its container.
- Forms use a readable constrained width, visible labels, helper text for risky
  settings, 44px minimum controls, inline validation, and a clear cancel path.
- Region/instance search controls and their selects have separate explicit
  labels. Secret values use password controls. Tables provide captions, scoped
  headers, long-value wrapping, and labelled mobile rows.
- Status is always text plus color. Buttons cover hover, focus, active,
  disabled, and htmx loading states.
- Empty states name the missing prerequisite and link to the exact next page.
- Motion is limited to 150-250ms state feedback and is disabled under
  `prefers-reduced-motion`.
- Login remains deliberately quiet and does not present authenticated
  navigation before login.
- Shared behavior moves to a small deferred `app.js`: active form disclosure,
  searchable select filtering, htmx loading feedback, accessible destructive
  confirmation via native dialog, SSE log handling, and copy-log actions. Core
  navigation and form submission continue to work without JavaScript.

## Local Operations And CI

`hermes doctor` performs non-destructive local checks: configuration validity,
Pulumi CLI availability/version, required provider plugins, and writable local
database/state parent directories. Checks return structured pass/warn/fail
lines and a non-zero exit code when Hermes cannot start or provision.

`make reset-local CONFIRM=reset` removes only repository-local SQLite files. It
preserves Pulumi state because deleting state can orphan live resources. A
separate, deliberately stronger `reset-local-state` command requires its own
confirmation, accepts only a repository-local `file://` backend, and refuses to
run when stack files are present unless the operator supplies an explicit
force acknowledgement. Neither command calls AWS.

`make check` builds CSS, runs unit tests, vet, and build. GitHub Actions performs
the same checks and verifies the committed generated CSS is current.

## Verification

- TDD for every behavior change: observe the focused test fail before writing
  production code, then run focused and full suites.
- Store, orchestrator, API, renderer, doctor, and command tests cover the new
  contracts without AWS.
- Browser verification uses seeded disposable local data and checks login,
  list/create/edit/duplicate/deploy pages, lifecycle error states, job detail,
  keyboard focus, and responsive layouts at 375, 768, 1024, and 1440 widths.
- Final gates: CSS build with no diff, `go test ./... -count=1`, `go vet ./...`,
  `go build ./...`, and independent spec/code-quality review.
- Real AWS tests remain out of this milestone and are the final acceptance gate.
