# Hermes Stable Local + UI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Hermes a reliable localhost provisioning console with atomic lifecycle transitions, actionable diagnostics, dedicated create/edit pages, blueprint editing/copying, safe local tooling, CI, and a polished responsive UI.

**Architecture:** SQLite transactions become the lifecycle authority: enqueue, cancel, start, and finish transitions compare old state and update Job plus Environment atomically. Server-rendered list, form, detail, and diagnostic pages share one restrained operations-console design system; htmx is limited to metadata, polling, partial updates, and destructive actions, while normal workflows use stable URLs and Post/Redirect/Get.

**Tech Stack:** Go 1.25, `net/http`, `html/template`, htmx, Tailwind CSS 4, SQLite, Pulumi Automation API, AWS SDK v2. UI work follows `@ui-ux-pro-max` and `@impeccable`; behavior work follows `@superpowers:test-driven-development`.

---

### Task 1: Atomic Store Lifecycle

**Files:**
- Create: `internal/store/migrations/0007_environment_resume_status.sql`
- Create: `internal/store/lifecycle.go`
- Create: `internal/store/lifecycle_test.go`
- Modify: `internal/store/environment.go`
- Modify: `internal/store/job.go`
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Step 1: Write failing migration and lifecycle tests**

Add tests covering:

```go
func TestEnqueueJobTransitionsEnvironmentAtomically(t *testing.T)
func TestEnqueueJobRejectsStaleEnvironmentStatus(t *testing.T)
func TestEnqueueJobRejectsConcurrentActiveJob(t *testing.T)
func TestDestroyAndCancelAreAtomic(t *testing.T)
func TestStartJobRequiresQueuedStatus(t *testing.T)
func TestCompleteJobUpdatesJobAndEnvironmentAtomically(t *testing.T)
func TestCompleteJobRollsBackOnStaleEnvironment(t *testing.T)
func TestFailOrphanCompletesQueuedOrRunningJob(t *testing.T)
func TestMigrationApplicationIsAtomic(t *testing.T)
```

The enqueue test must assert that a single transaction creates a queued Job,
moves the Environment to its transient status, and captures `resume_status`
when starting destroy preview. The race test runs Destroy and Cancel against
the same `destroy_preview_ready` row and asserts exactly one transition wins.

**Step 2: Run the tests and observe RED**

Run:

```bash
go test ./internal/store -run 'Test(EnqueueJob|DestroyAndCancel|StartJob|CompleteJob|FailOrphan|MigrationApplication)' -count=1 -v
```

Expected: compile failures for the new lifecycle API and missing resume column.

**Step 3: Add the migration and store API**

The migration adds:

```sql
ALTER TABLE environments ADD COLUMN resume_status TEXT NOT NULL DEFAULT '';
```

Extend `Environment` reads with `ResumeStatus`. Implement these contracts in
`lifecycle.go`:

```go
var ErrStaleTransition = errors.New("environment state changed")
var ErrJobNotQueued = errors.New("job is not queued")

type EnqueueTransition struct {
    EnvironmentID int64
    Action string
    AllowedFrom []string
    TransientStatus string
    CaptureResumeStatus bool
}

func (s *Store) EnqueueJobTransition(ctx context.Context, in EnqueueTransition) (int64, error)
func (s *Store) StartJob(ctx context.Context, jobID int64) (Job, Environment, error)

type JobCompletion struct {
    JobID, EnvironmentID int64
    JobStatus, EnvironmentStatus string
    Logs, Error string
    Summary map[string]any
    Outputs map[string]any
    ClearResumeStatus bool
}

func (s *Store) CompleteJob(ctx context.Context, in JobCompletion) error
func (s *Store) CancelDestroyPreview(ctx context.Context, environmentID int64) error
func (s *Store) FailOrphanJob(ctx context.Context, jobID, environmentID int64, message string) error
```

Use `BEGIN`, conditional state checks, the existing partial unique index, and
rollback on every error. Enqueue changes status before returning, eliminating
the queued-action/cancel window. `CancelDestroyPreview` restores the captured
resume status and clears it in the same transaction.

Refactor migration execution so migration body plus schema version insert share
one transaction.

**Step 4: Run focused and package tests**

```bash
go test ./internal/store -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/store
git commit -m "🏗️ refactor(store): make lifecycle transitions atomic"
```

---

### Task 2: Orchestrator Policy And Durable Completion

**Files:**
- Create: `internal/orchestrator/lifecycle.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/orchestrator/orchestrator_test.go`
- Modify: `internal/orchestrator/broker.go`
- Modify: `internal/orchestrator/broker_test.go`
- Modify: `cmd/hermes/main.go`

**Step 1: Write failing orchestrator tests**

Add table-driven tests:

```go
func TestEnqueueStateActionMatrix(t *testing.T)
func TestEnqueueRejectsUnknownAction(t *testing.T)
func TestRetryRequiresFailedEnvironment(t *testing.T)
func TestRetryUsesLatestFailedJob(t *testing.T)
func TestRetryRejectsMissingFailedJob(t *testing.T)
func TestCancelDestroyPreviewUsesAtomicStoreTransition(t *testing.T)
func TestRunDoesNotCallProvisionerWhenStartTransitionFails(t *testing.T)
func TestRunRejectsUnknownPersistedAction(t *testing.T)
func TestRunPanicPersistsFailureAndFinalLogs(t *testing.T)
func TestStopPersistsInterruptedJobBeforeReturning(t *testing.T)
func TestRecoverOrphansReturnsError(t *testing.T)
func TestBrokerCloseReleasesTerminalTopic(t *testing.T)
```

The state matrix is:

```text
pending -> preview -> previewing
preview_ready -> up -> provisioning
preview_ready|up|failed -> destroy_preview -> destroy_previewing
up -> refresh -> refreshing
destroy_preview_ready -> destroy -> destroying
failed -> Retry(latest failed action) -> action transient state
```

All transient and destroyed states reject new work. A failed Destroy may only
be repeated through `Retry`; other failed actions require retry or destroy
preview.

**Step 2: Run tests and observe RED**

```bash
go test ./internal/orchestrator -run 'Test(EnqueueState|EnqueueRejects|Retry|CancelDestroy|RunDoesNot|RunRejects|RunPanic|StopPersists|RecoverOrphans|BrokerClose)' -count=1 -v
```

Expected: failures because Enqueue only checks busy state and worker writes are
not atomic.

**Step 3: Implement policy and durable worker finalization**

Define typed, safe errors:

```go
var ErrEnvironmentBusy = errors.New("environment already has an active job")
var ErrInvalidAction = errors.New("unknown environment action")
var ErrInvalidTransition = errors.New("action is not allowed in the current environment state")
var ErrNoFailedJob = errors.New("environment has no failed job to retry")
```

`Enqueue` resolves policy then calls `Store.EnqueueJobTransition`; it no longer
changes Environment status in the worker. Add:

```go
func (o *Orchestrator) Retry(ctx context.Context, envID int64) (int64, error)
func (o *Orchestrator) CancelDestroyPreview(ctx context.Context, envID int64) error
```

`run` calls `StartJob`, invokes exactly one known provisioner method, prepares
the complete terminal payload, snapshots logs, and calls `CompleteJob`. Every
failure and panic uses `context.WithoutCancel` plus a short timeout to preserve
the final error and logs after shutdown cancellation. Critical store errors are
returned/logged, never discarded.

Change `Start(ctx)` to return orphan recovery errors; main refuses to serve if
recovery cannot be persisted. Release broker topics after terminal logs have
been snapshotted because SQLite is now the history source.

**Step 4: Run focused and dependent tests**

```bash
go test ./internal/orchestrator ./internal/api ./cmd/hermes -count=1
```

Expected: PASS after API compile adjustments needed only for changed method
signatures; behavior changes stay for Task 3.

**Step 5: Commit**

```bash
git add internal/orchestrator cmd/hermes/main.go
git commit -m "🦺 feat(orchestrator): enforce lifecycle transitions"
```

---

### Task 3: Lifecycle HTTP Errors And Reliable SSE

**Files:**
- Modify: `internal/api/environments.go`
- Modify: `internal/api/environments_test.go`
- Modify: `internal/api/jobs.go`
- Modify: `internal/api/jobs_test.go`
- Modify: `internal/api/blueprints.go`
- Modify: `internal/api/blueprints_test.go`
- Modify: `internal/store/environment.go`
- Test: `internal/store/environment_test.go`

**Step 1: Write failing API tests**

Add:

```go
func TestEnvironmentActionsRejectInvalidStates(t *testing.T)
func TestEnvironmentActionShowsBusyReason(t *testing.T)
func TestRetryDoesNotDefaultToUp(t *testing.T)
func TestCancelDestroyPreviewCannotRaceQueuedDestroy(t *testing.T)
func TestDeployPreviewEnqueueFailureLeavesNoPendingEnvironment(t *testing.T)
func TestJobLogStreamReturnsPersistedTerminalLogs(t *testing.T)
func TestJobLogStreamRejectsUnknownJob(t *testing.T)
func TestJobLogStreamUsesBrokerOnlyForActiveJob(t *testing.T)
```

POST action failures must redirect back with a safe flash/error signal and
create no Job. A terminal SSE request must replay DB logs, emit `done`, and
return. Unknown IDs must return 404 without creating a broker topic.

**Step 2: Run and observe RED**

```bash
go test ./internal/api -run 'Test(EnvironmentAction|RetryDoes|CancelDestroy|DeployPreview|JobLogStream)' -count=1 -v
```

**Step 3: Implement handlers**

- Replace ignored `Enqueue` results with a shared redirect helper that maps
  typed lifecycle errors to concise Chinese recovery messages.
- `retryHandler` calls `Orchestrator.Retry`; it never guesses `up`.
- `cancelDestroyHandler` calls the atomic orchestrator method.
- Direct Destroy has no handler-local status exception; policy owns it.
- Add `Store.DeletePendingEnvironment` and compensate if initial Preview enqueue
  fails after Environment creation. It must delete only `pending` rows with no
  Jobs.
- Load a Job before subscribing to SSE. Terminal Jobs replay persisted logs;
  active Jobs subscribe to Broker; missing Jobs return 404.

**Step 4: Run API and full tests**

```bash
go test ./internal/api ./internal/orchestrator ./internal/store -count=1
```

**Step 5: Commit**

```bash
git add internal/api internal/store/environment.go internal/store/environment_test.go
git commit -m "🐛 fix(api): surface lifecycle failures and terminal logs"
```

---

### Task 4: App Shell And Dedicated Account/Project Pages

**Files:**
- Create: `internal/api/accounts.go`
- Create: `internal/web/templates/account_form.html`
- Create: `internal/web/templates/project_form.html`
- Create: `internal/web/static/app.js`
- Modify: `internal/api/server.go`
- Modify: `internal/api/accounts_test.go`
- Modify: `internal/api/projects.go`
- Modify: `internal/api/projects_test.go`
- Modify: `internal/web/web.go`
- Modify: `internal/web/web_test.go`
- Modify: `internal/web/templates/layout.html`
- Modify: `internal/web/templates/accounts.html`
- Modify: `internal/web/templates/projects.html`
- Modify: `internal/web/templates/login.html`
- Modify: `internal/web/templates/_account_rows.html`
- Modify: `internal/web/templates/_fragments.html`

**Step 1: Write failing route/render tests**

Add tests asserting:

```go
func TestAccountListContainsDataButNoCreateForm(t *testing.T)
func TestNewAccountPageRendersDedicatedForm(t *testing.T)
func TestCreateAccountStoresDefaultRegion(t *testing.T)
func TestCreateAccountValidationPreservesSafeFieldsOnly(t *testing.T)
func TestProjectListContainsDataButNoCreateForm(t *testing.T)
func TestNewProjectPageRendersDedicatedForm(t *testing.T)
func TestCreateProjectValidationReturns422AndPreservesInput(t *testing.T)
func TestLoginLayoutHidesAuthenticatedNavigation(t *testing.T)
func TestDeepPageMarksActiveNavigation(t *testing.T)
func TestLayoutProvidesSkipLinkAndDialog(t *testing.T)
```

Account validation must preserve alias, region, and Access Key ID but never
render the submitted Secret Access Key. Successful creates use 303.

**Step 2: Run and observe RED**

```bash
go test ./internal/api ./internal/web -run 'Test(AccountList|NewAccount|CreateAccount|ProjectList|NewProject|CreateProjectValidation|LoginLayout|DeepPage|LayoutProvides)' -count=1 -v
```

**Step 3: Implement page data and routes**

Move account handlers from `server.go` into `accounts.go`. Add:

```text
GET /accounts/new
POST /accounts
GET /projects/new
POST /projects
```

Trim and validate names/region before external validation. Persist
`DefaultRegion`; use Post/Redirect/Get on success and semantic 422/409 form
responses on validation/duplicate errors.

Register new templates in `web.go`. Pass `PageTitle`, `ActiveNav`, and `HideNav`
to layout data. Add a skip link, `aria-current="page"`, a quiet login shell,
deferred `/static/app.js`, and a shared native confirmation dialog. List pages
contain only heading/action, empty state, table, and row actions.

**Step 4: Run focused tests and build static assets**

```bash
go test ./internal/api ./internal/web -count=1
npm run css:build
```

**Step 5: Commit**

```bash
git add internal/api internal/web
git commit -m "✨ feat(web): separate account and project forms"
```

---

### Task 5: Blueprint Detail, Edit, Copy, And Deploy Pages

**Files:**
- Create: `internal/web/templates/blueprint_form.html`
- Create: `internal/web/templates/blueprint_detail.html`
- Create: `internal/web/templates/blueprint_deploy.html`
- Modify: `internal/store/blueprint.go`
- Modify: `internal/store/blueprint_test.go`
- Modify: `internal/api/blueprints.go`
- Modify: `internal/api/blueprints_test.go`
- Modify: `internal/api/metadata.go`
- Modify: `internal/api/metadata_test.go`
- Modify: `internal/web/web.go`
- Modify: `internal/web/web_test.go`
- Modify: `internal/web/templates/blueprints.html`
- Modify: `internal/web/templates/_fragments.html`
- Modify: `internal/web/static/app.js`

**Step 1: Write failing store and route tests**

Add:

```go
func TestUpdateBlueprintRoundTripsParams(t *testing.T)
func TestUpdateBlueprintNotFound(t *testing.T)
func TestBlueprintEditDoesNotMutateEnvironmentSnapshot(t *testing.T)
func TestBlueprintListContainsNoCreateOrDeployForm(t *testing.T)
func TestNewBlueprintPageRendersDefaults(t *testing.T)
func TestEditBlueprintPagePrefillsAllFields(t *testing.T)
func TestUpdateBlueprintPersistsValidatedFields(t *testing.T)
func TestDuplicateBlueprintPageDoesNotWriteAndPrefillsCopy(t *testing.T)
func TestDuplicateBlueprintSaveCreatesSecondRecord(t *testing.T)
func TestBlueprintDeployPageRequiresEnvironmentName(t *testing.T)
func TestMetadataOptionsPreserveSelectedLegacyValues(t *testing.T)
func TestBlueprintPrerequisitesLinkToAccountAndProjectCreation(t *testing.T)
```

**Step 2: Run and observe RED**

```bash
go test ./internal/store ./internal/api ./internal/web -run 'Test(UpdateBlueprint|BlueprintEdit|BlueprintList|NewBlueprint|EditBlueprint|DuplicateBlueprint|BlueprintDeploy|MetadataOptions|BlueprintPrerequisites)' -count=1 -v
```

**Step 3: Implement shared blueprint form model**

Add `UpdateBlueprint` with rows-affected not-found handling. Centralize parsing
and defaults in a `blueprintFormData`/parser used by create and update.

Register:

```text
GET  /blueprints/new
POST /blueprints
GET  /blueprints/{id}
GET  /blueprints/{id}/edit
POST /blueprints/{id}
GET  /blueprints/{id}/duplicate
GET  /blueprints/{id}/deploy
POST /blueprints/{id}/deploy
```

Duplicate GET changes form mode/action and proposes a copy name without writing.
Edit warns that existing Environment snapshots do not change. Deploy shows a
resource summary and uses a short dedicated form.

Metadata endpoints accept explicit selected region, instance type, and AMI.
Option writers insert a selected legacy value when it is missing from cache so
edit/copy never silently changes saved data after htmx swaps.

The long form groups ownership, compute/access, VPC, MySQL, and Redis. Optional
groups use progressive disclosure; Redis AUTH is disabled until Redis is on.

**Step 4: Run focused and full affected tests**

```bash
go test ./internal/store ./internal/api ./internal/web -count=1
npm run css:build
```

**Step 5: Commit**

```bash
git add internal/store/blueprint* internal/api/blueprints* internal/api/metadata* internal/web
git commit -m "✨ feat(blueprints): add edit copy and deploy pages"
```

---

### Task 6: Job History And Actionable Diagnostics

**Files:**
- Create: `internal/web/templates/job_detail.html`
- Modify: `internal/store/job.go`
- Modify: `internal/store/job_test.go`
- Modify: `internal/api/jobs.go`
- Modify: `internal/api/jobs_test.go`
- Modify: `internal/api/environments.go`
- Modify: `internal/api/environments_test.go`
- Modify: `internal/web/templates/environment_detail.html`
- Modify: `internal/web/templates/_fragments.html`
- Modify: `internal/web/web.go`
- Modify: `internal/web/web_test.go`
- Modify: `internal/web/static/app.js`

**Step 1: Write failing summary/history tests**

Add:

```go
func TestListJobSummariesExcludesLogs(t *testing.T)
func TestGetLatestFailedJobSkipsNewerSuccessfulJobs(t *testing.T)
func TestEnvironmentDetailShowsAllJobSummariesNewestFirst(t *testing.T)
func TestEnvironmentJobRowsPollOnlyWhileActive(t *testing.T)
func TestEnvironmentDetailDoesNotOpenSSEForTerminalJob(t *testing.T)
func TestEnvironmentDetailShowsFailedActionAndRecovery(t *testing.T)
func TestJobDetailShowsMetadataSummaryErrorAndLogs(t *testing.T)
func TestJobDetailReturns404ForUnknownJob(t *testing.T)
```

Use a SQLite trace/test assertion or a large sentinel log to prove the summary
query does not select or materialize `logs`.

**Step 2: Run and observe RED**

```bash
go test ./internal/store ./internal/api ./internal/web -run 'Test(ListJobSummaries|GetLatestFailed|EnvironmentDetail|EnvironmentJobRows|JobDetail)' -count=1 -v
```

**Step 3: Implement lightweight views and diagnostics**

Add `JobSummary` and queries whose columns omit `logs`, plus
`GetLatestFailedJob`. Keep `GetJob` as the only historical full-log read.

Add `GET /jobs/{id}` and `GET /environments/{id}/jobs`. The environment page
shows translated action/status, timestamps, duration, summary, truncated error,
and a detail link. The fragment polls only while a Job is queued/running and
omits `hx-trigger` when stable.

Only an active Job renders the live log panel with a stream URL data attribute.
`app.js` owns EventSource lifecycle, avoids duplicated persisted text, announces
completion, and refreshes status/history. Job detail provides a copy-log command.

**Step 4: Run focused and full tests**

```bash
go test ./internal/store ./internal/api ./internal/web -count=1
npm run css:build
```

**Step 5: Commit**

```bash
git add internal/store/job* internal/api internal/web
git commit -m "✨ feat(jobs): add history and diagnostic details"
```

---

### Task 7: Localhost Defaults, Doctor, Safe Reset, And CI

**Files:**
- Create: `internal/localops/doctor.go`
- Create: `internal/localops/doctor_test.go`
- Create: `internal/localops/reset.go`
- Create: `internal/localops/reset_test.go`
- Create: `cmd/hermes/commands.go`
- Create: `cmd/hermes/commands_test.go`
- Create: `.github/workflows/ci.yml`
- Modify: `cmd/hermes/main.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `Makefile`
- Modify: `.env.example`
- Modify: `README.md`

**Step 1: Write failing local operations tests**

Add:

```go
func TestLoadDefaultsToLoopback(t *testing.T)
func TestDoctorReportsMissingPulumi(t *testing.T)
func TestDoctorReportsMissingAWSAndRandomPlugins(t *testing.T)
func TestDoctorDoesNotCallAWS(t *testing.T)
func TestResetRequiresConfirmation(t *testing.T)
func TestResetRemovesDatabaseSidecarsButPreservesState(t *testing.T)
func TestResetRejectsPathOutsideRepository(t *testing.T)
func TestResetRejectsSymlinkEscape(t *testing.T)
func TestPurgeStateRejectsS3AndOutsidePaths(t *testing.T)
func TestPurgeStateRequiresExplicitForceWhenStacksExist(t *testing.T)
func TestCLISelectsDoctorAndResetCommands(t *testing.T)
```

Command execution is injected in doctor tests. Parse `pulumi plugin ls --json`
structurally; never make an AWS API call.

**Step 2: Run and observe RED**

```bash
go test ./internal/config ./internal/localops ./cmd/hermes -count=1 -v
```

**Step 3: Implement commands and automation**

- Default `HERMES_ADDR` to `127.0.0.1:8080`.
- Add `hermes doctor`, `hermes reset-local --confirm`, and the stronger
  `hermes reset-local-state --confirm --force` command.
- Reset removes only DB plus `-wal`, `-shm`, and `-journal`; state purge refuses
  S3, repository-external paths, symlink escapes, and stack files without force.
- Add Make targets `doctor`, `reset-local`, `reset-local-state`, and `check`.
- Update local docs; state clearly that deleting state never deletes AWS resources.
- CI runs `npm ci`, CSS build, generated-CSS diff, gofmt diff/check, unit tests,
  vet, and build. It does not run integration or AWS tests.

**Step 4: Verify local commands and CI contract**

```bash
go test ./internal/config ./internal/localops ./cmd/hermes -count=1
make check
git diff --exit-code internal/web/static/app.css
```

**Step 5: Commit**

```bash
git add internal/localops cmd/hermes internal/config Makefile .env.example README.md .github
git commit -m "🧑‍💻 chore(local): add doctor reset and CI checks"
```

---

### Task 8: Responsive UI Polish And Browser Acceptance

**Files:**
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/static/app.css` (generated)
- Modify: `internal/web/static/app.js`
- Modify: `internal/web/templates/*.html`
- Modify: `internal/web/web_test.go`
- Modify: `DESIGN.md` only if implementation introduces a deliberate token change

**Step 1: Write failing renderer accessibility tests**

Assert the rendered surfaces include:

```go
func TestTablesHaveCaptionsAndScopedHeaders(t *testing.T)
func TestFormsHaveExplicitLabelsAndErrorRegions(t *testing.T)
func TestStatusBadgesIncludeText(t *testing.T)
func TestDestructiveActionsUseSharedConfirmation(t *testing.T)
func TestLongIdentifiersUseWrappingUtilities(t *testing.T)
func TestGeneratedAssetsIncludeResponsiveAndReducedMotionRules(t *testing.T)
```

**Step 2: Run renderer tests and observe RED**

```bash
go test ./internal/web -run 'Test(TablesHave|FormsHave|StatusBadges|DestructiveActions|LongIdentifiers|GeneratedAssets)' -count=1 -v
```

**Step 3: Apply the final design pass**

Following `@ui-ux-pro-max` and `@impeccable`:

- remove floating page-card styling and nested-card composition;
- retain the committed steel-blue/system-font identity;
- use at least 16px mobile inputs and 44px interaction targets;
- strengthen border/text contrast to WCAG AA pairs;
- give every interactive control hover/focus/active/disabled/loading states;
- add table captions, scoped headers, labelled narrow-screen rows, and safe long
  identifier wrapping;
- make empty states prerequisite-aware and actionable;
- use text plus semantic color for all status states;
- keep animation to state feedback and honor reduced motion;
- verify no element overlaps, clips, or shifts when status/log content changes.

Do not add marketing heroes, decorative gradients, glass, oversized metrics,
font downloads, ornamental cards, or a new frontend framework.

**Step 4: Build assets and run automated checks**

```bash
npm run css:build
gofmt -w cmd internal
go test ./... -count=1
go vet ./...
go build ./...
git diff --check
```

Expected: all pass, and generated CSS is committed.

**Step 5: Run browser acceptance**

Start Hermes with a disposable DB and non-production local state. Do not add AWS
credentials or invoke provisioning. Seed non-secret list/Job data directly for
read-only UI coverage.

Use `@browser:control-in-app-browser` to verify:

1. login and authenticated navigation;
2. empty and populated list pages;
3. account/project create pages;
4. blueprint new/detail/edit/duplicate/deploy pages;
5. environment stable, active, failed, and destroyed views;
6. Job detail and copy-log behavior;
7. keyboard navigation, visible focus, dialog escape, and reduced motion;
8. screenshots at 375x812, 768x1024, 1024x768, and 1440x900;
9. no horizontal page overflow, overlap, clipped text, blank regions, or console errors.

**Step 6: Independent reviews**

Request a spec-compliance review against the design and this plan, then a code
quality review. Fix all Critical and Important findings and re-run the full
verification suite.

**Step 7: Commit**

```bash
git add internal/web DESIGN.md
git commit -m "💄 style(web): polish the operations console"
```

---

## Completion Gate

- Every new behavior was demonstrated RED before implementation and GREEN after.
- All eight tasks passed spec and code-quality review.
- `make check`, full Go tests, vet, build, CSS generation, and diff checks pass.
- Browser screenshots prove the required routes at all four viewports.
- No real AWS command or integration test was run.
- The list pages contain data and actions only; every create/edit workflow uses
  a dedicated page.

