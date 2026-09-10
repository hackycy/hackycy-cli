# CLI Terminal Experience

This context names the user-visible parts of one ycy command invocation so
interactive presentation, durable output, and diagnostics remain distinct.

## Language

**Terminal Experience**:
The complete user-facing lifecycle of one command invocation, from its first
prompt or status through its final outcome.
_Avoid_: UI output, dialog

**Live View**:
The temporary interactive view shown while a command is asking questions or
reporting active work. It is not itself a durable record of the invocation.
_Avoid_: Transcript, result

**Interaction Transcript**:
A redacted, durable account of completed prompts, meaningful work milestones,
and the final outcome that remains available in normal terminal history.
_Avoid_: Raw screen capture, log

**Interaction Milestone**:
A command-selected semantic checkpoint that is shown during the invocation and
retained in the Interaction Transcript after the temporary Live View ends.
_Avoid_: Notice, animation frame, request log

**Discovery Document**:
A durable description of one root or command-group surface, including its
usage, direct commands, options, and examples.
_Avoid_: Live View, completion script, parser error

**Environment Selection**:
The ordered choice of dotenv files to read for one export, including whether
the base `.env` is automatically merged before an environment-specific file.
_Avoid_: Environment value, JSON result, file contents

**Fork Instance Projection**:
A secret-safe listing record containing a configured provider instance's name,
host, protocol, provider type, and non-decrypting token preview.
_Avoid_: Fork credentials, ciphertext, provider session

**Fork Instance Setup**:
The ordered interaction and persistence flow that collects a provider alias,
host, provider type, protocol, and access token for one configured instance.
_Avoid_: Fork credentials, provider login, arbitrary configuration edit

**CM Profile Projection**:
A secret-safe listing record for one commit-message provider profile containing
its name, Base URL, model, and whether it matches the stored default profile;
it never contains a decrypted API key.
_Avoid_: Resolved CM profile, provider credential, API key

**CM Profile Setup**:
The ordered interaction and persistence flow that collects a profile name,
OpenAI-compatible Base URL, model, and API key for one commit-message
provider profile.
_Avoid_: Resolved CM profile, provider session, arbitrary configuration edit

**CM Provider Test**:
A non-mutating connection check that resolves one CM profile and asks its
OpenAI-compatible provider for a minimal deterministic response.
_Avoid_: CM Profile Setup, provider credential change, commit generation

**Default CM Profile Selection**:
The atomic operation that validates the exact name of a stored CM profile and
persists it as the default selection without resolving or decrypting provider
credentials.
_Avoid_: Runtime profile resolution, temporary profile choice, provider login

**CM Profile Setting Update**:
An atomic request to apply one exact key and value to a stored CM profile using
that key's established normalization, parsing, or credential-encryption rules;
validation failure publishes no configuration change.
_Avoid_: CM Profile Setup, default profile selection, arbitrary config edit

**CM Profile Removal**:
The validated, default-negative deletion of one named CM profile, including
the storage rule that selects the first remaining profile or clears the default
when the removed profile was current.
_Avoid_: CM Profile Setting Update, profile deactivation, provider logout

**Work Phase**:
A meaningful stage of finite command work whose active and final states are
visible to the user and whose final state belongs in the Interaction Transcript.
_Avoid_: Spinner frame, log line

**Service Command**:
A command that remains active to operate a local service or connection until it
is interrupted. Its continuing experience is a Lifecycle Log, not a Live View.
_Avoid_: Long task, background command

**Tunnel Connection Selection**:
The finite interaction that resolves an ambiguous remembered Tunnel connection
before a Tunnel Client Session starts. It is not part of the continuing service
view and never identifies a candidate by exposing credential material.
_Avoid_: Tunnel Client Session, authentication, connection log

**Tunnel Client Session**:
One foreground `tunnel connect` service lifecycle after configuration has been
resolved, spanning instance ownership, authenticated control connections,
desired-state reconciliation, FRP supervision, reconnects, and final shutdown.
_Avoid_: Tunnel Connection Selection, one control connection, FRP process

**Tunnel Control Connection**:
One authenticated control-plane connection within a Tunnel Client Session. A
session may own several sequential control connections as transient failures
are retried without becoming several sessions.
_Avoid_: Tunnel Client Session, connection attempt, FRP connection

**Tunnel Desired-State Reconciliation**:
The ordered application of one authenticated desired revision to the managed
FRP state, including restoration of the prior applied state when activation
cannot be completed.
_Avoid_: Control message, configuration write, FRP restart

**Tunnel Failure Window**:
The interval from one transient control or FRP failure until the corresponding
connection or process is restored, shutdown is requested, or the failure is
classified as fatal. Repeated retry details belong inside one window rather
than becoming repeated normal-level warnings.
_Avoid_: Single retry attempt, Client Session, outage duration

**Tunnel Server Session**:
One foreground `tunnel server` service lifecycle owning the server state,
control-plane listener, authenticated browser/agent services, and managed FRPS
process until cancellation, listener failure, or explicit shutdown.
_Avoid_: Tunnel Client Session, browser session, FRP child process

**Managed FRPS Lifecycle**:
The server-owned preparation, publication, verification, activation,
supervision, recovery, and stopping of the pinned FRPS process. Its status is
part of the Tunnel Server Session but is independent from HTTP listener health.
_Avoid_: Raw FRPS output, control-plane listener, browser request

**Tunnel Agent Session**:
One authenticated client-agent control connection accepted by a Tunnel Server
Session after its hello and welcome exchange. A trusted client may establish
several sequential sessions as temporary connections fail and recover.
_Avoid_: Browser session, Tunnel Client Session, WebSocket request

**Control-Plane Change**:
A durable Tunnel server domain mutation that has committed successfully, such
as an account, trusted-client, tunnel, or managed-server setting change. It is
not the HTTP request that attempted the mutation.
_Avoid_: HTTP request, validation failure, database statement

**Archive Planning**:
The ordered `zip` interaction that selects a workspace package, source
directory, file patterns, and output name before any archive files are read or
written.
_Avoid_: Archive Collection, Archive Publication, zip result

**Archive Collection**:
The bounded discovery of regular files matching the selected `zip` patterns,
including the command's hidden-entry and output-file exclusions.
_Avoid_: Archive Planning, compression, filesystem walk log

**Archive Publication**:
The one-time write and optional host reveal of a completed ZIP artifact after
collection and compression have succeeded.
_Avoid_: Archive Collection, output-name planning, host shell session

**Release Resolution**:
The strict comparison of the running ycy version with latest release metadata
and selection of one supported platform artifact plus its checksum source.
_Avoid_: Download Candidate, Update Transaction, startup result

**Update Candidate**:
A same-directory release artifact that has completed download, checksum,
permission/quarantine handling, and plain version self-check before scheduling.
_Avoid_: Release Resolution, installed binary, Update Transaction

**Update Transaction**:
The persisted parent/hidden-updater agreement that records a pending
replacement and its eventual success, cleanup warning, or rollback failure.
_Avoid_: Release Resolution, Lifecycle Log, Command Result

**Startup Transaction Result**:
The one-time summary consumed by a later ordinary ycy invocation after a
detached update attempt, including success, cleanup warning, pending, or
rollback outcome.
_Avoid_: Scheduled result, Update Transaction, Diagnostic Record

**Behavior Baseline**:
The captured pre-migration contract for one command's help, streams, exit,
side effects, schemas, signals, and process/state boundaries against which a
terminal presentation change is compared.
_Avoid_: Visual Review Record, Command Result, implementation snapshot

**Visual Review Record**:
The human-checkable evidence for a terminal scenario's dimensions, capability,
input path, expected semantic states, and known visual differences.
_Avoid_: ANSI golden contract, Behavior Baseline, screenshot-only approval

**Terminal Experience Rollout Gate**:
The required automated and human evidence that permits one shared terminal or
command presentation slice to advance without changing its established CLI
behavior or safety boundaries.
_Avoid_: Release Candidate, feature flag, unit test alone

**Lifecycle Log**:
The readable, line-oriented account of meaningful state changes, warnings, and
failures emitted while a Service Command is running.
_Avoid_: Interaction Transcript, request log

**Comparison Refresh Attempt**:
One accepted attempt to discover, compare, and atomically publish a new
immutable Comparison Snapshot for a fixed Comparison Workspace.
_Avoid_: HTTP request, progress update, Comparison Snapshot

**FS Managed Task**:
A download, archive extraction, or chunked upload with its own identity and a
lifecycle that can span multiple File Browser requests.
_Avoid_: HTTP request, thumbnail generation, synchronous file operation

**Git Heat Report**:
A ranked projection of file or immediate-directory change activity across one
selected Git history range, including change kinds and each path's latest time.
_Avoid_: Git log, file statistics, repository scan

**Git Pulse Report**:
A repository-grouped projection of commits found across one workspace calendar
range after the command's optional author selection has been applied.
_Avoid_: Git Commit Tree, repository log, workspace scan

**Repository Fetch Omission**:
A discovered Git repository whose history could not be read and is therefore
absent from an otherwise successful Git Pulse Report.
_Avoid_: Command failure, empty repository, filtered repository

**Git Fork Acquisition**:
An archive-first attempt to create a history-free repository working tree,
including its shallow-clone fallback and required Git-metadata removal.
_Avoid_: Git clone, repository download, Fork Instance Setup

**Destination Replacement**:
The confirmed recursive removal of an existing nonempty Git Fork destination
before a Git Fork Acquisition begins.
_Avoid_: File overwrite, destination cleanup, clone retry

**Git CM Workflow**:
The scope-bound flow that may select and stage changes, generate one validated
commit message, create its commit, and optionally push it.
_Avoid_: Commit generation, Git automation, CM Provider Test

**Command Result**:
The durable outcome intended for the command's caller, including redirected or
machine-consumed output. It remains independent from interactive presentation.
_Avoid_: Transcript, diagnostic

**Diagnostic Record**:
Operational information intended to explain failures or support debugging,
without changing the Command Result.
_Avoid_: Result, transcript
