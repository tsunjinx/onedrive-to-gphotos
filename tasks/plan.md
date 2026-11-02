# OneDrive to Google Photos CLI — implementation and validation plan

Updated: 2026-10-19. Native Graph and legacy rclone transfers, encrypted checkpoints, parallel uploads with sequential Google creation batches, bilingual menu and remembered settings are implemented. Owner evidence includes photo metadata retention and a reconciled single-video creation; live READY/playback/audio, multi-item cloud recovery, native Windows and power-loss acceptance remain open. Graph read-only requests now have bounded throttling retries, and new plans support explicit folder upload exclusions. See [review evidence](review.md) and [operating instructions](../docs/runbook.md).

## Current stage and next gates

| Stage | Status | Exit criterion |
|---|---|---|
| Setup and authentication | Implemented; owner reports success | Both auth check commands succeed for the intended accounts |
| Inventory and transfer engine | Implemented; owner completed root inventory with explicit Vault exclusion | Saved-job inventory completes and selected-folder counts reconcile with source and destination |
| Retry and restart | Implemented; simulated crash/interruption tests pass | Real interruption resumes the same job without repeating committed work |
| Representative media trial | After inventory recovery | Photos display correctly; videos reach READY with correct playback/audio/dates |
| Controlled larger run | Pending trial | A non-overlapping folder or the same saved full-library job completes with an explainable report and bounded disk usage |
| Approximately 700 GB migration | Pending preceding gates | Eligible items finish; skipped/failed items are reviewed; destination capacity remains sufficient |
| Native release acceptance | Pending | Windows/macOS runtime, refresh/keyring and disposable-VM power-loss checks pass |

Follow this order: check both accounts and available storage → select scope → preview → small live trial → inspect media and report → interrupt/resume the same job → larger run → full migration → final reconciliation. Public distribution remains outside scope. Parallel file transfers are implemented; creation batching is implemented; live throughput remains to be measured. The interactive menu now offers unfinished jobs as the default continuation choice.

Jobs have a fixed source folder and account/client binding. A new plan creates independent history; it cannot expand or resume a previous job. Use the same job ID for a pilot and its continuation, or use disjoint folder jobs. A root plan after a separate folder trial includes that folder again; do not assume cross-job or whole-library duplicate detection.

## Latest observed job and immediate recovery

The owner selected the drive through menu option 2 and planned `/` with a **10 GiB** staging cap. Job `84f8505d28b7ff00c8aab99f1c69533d` repeatedly stopped after 1,865 items. The read-only diagnostic identified the blocker: `Personal Vault` cannot be listed (`invalidRequest: invalidResourceId: ObjectHandle is Invalid`). This matches the [documented rclone OneDrive limitation](https://github.com/rclone/rclone/blob/master/docs/content/onedrive.md). No media was uploaded by these incomplete scans.

The explicit `--exclude-personal-vault` option uses the documented root filter `/Personal Vault/**` and disables ListR so filtering applies before descending into that folder. It is never enabled implicitly. For this incomplete root job, the operator can save the exclusion and continue with the same accounts/drive, ID and 10 GiB cap:

```bash
bash scripts/od2gp.sh run --job 84f8505d28b7ff00c8aab99f1c69533d --exclude-personal-vault --wait
```

The scan restarts, then displays counts and asks for upload approval. The exclusion persists for later continuation without repeating the flag. A completed inventory cannot have its exclusion scope changed through this option. Status and JSON/CSV reports explicitly mark `/Personal Vault` as an excluded folder with unknown file count/size; do not treat its contents as scanned, copied or individually skipped. Ordinary nested folders named `Personal Vault` are not excluded. Files actually inside the inaccessible Vault require separate handling and remain outside this job.

Immediate exit criterion: the root inventory completes with the explicit Vault exclusion and the owner reviews that reduced scope, eligible/skipped counts, storage and file sizes. Then perform a short controlled trial within this same job, interrupt and continue it for the remaining accessible library. Do not create an overlapping root plan as a shortcut. The same exclusion can be checked without modifying a job:

```bash
bash scripts/od2gp.sh source scan --exclude-personal-vault --diagnostics /
```

## Already-uploaded tracking and duplicate boundaries

The encrypted local journal is the source of upload progress **within a saved job**. Keep the job directory and its OS credential-store keys through completion; deleting either may prevent recovery. Reopening the CLI alone does not erase progress or start uploads.

| Recorded situation | Behavior when continuing the same job |
|---|---|
| Photo creation confirmed and completion committed | Skip that completed item; do not upload it again |
| Video created with a saved destination ID, still processing | Check processing status using that ID; wait for READY rather than upload again |
| File upload interrupted before confirmed creation | Recover its recorded state and query Google's accepted offset; expired sessions may require restarting that unfinished upload |
| Creation response lost or local completion commit uncertain | Pause for explicit reconciliation; do not assume either success or failure and blindly repeat creation |
| Inventory never completed | Restart discovery before uploading; partial counts are not a complete plan |

A new plan has independent history. There is no shared completion ledger across jobs and no automatic comparison against the whole existing Google Photos library, including media uploaded manually or by other applications. Current content hashes support integrity/recovery; they are not a library-wide duplicate index. Do not promise exactly-once delivery solely from local checkpoints. `resume --reconcile` is an explicit recovery path for uncertain creation, and real provider behavior remains an acceptance gate.

Acceptance before scaling: interrupt the same trial job after confirmed photo uploads and while a video is processing; verify completed photos are skipped, the saved video ID is polled, and unfinished files continue. Exercise a lost creation response with controlled failure simulation and confirm normal resume stops for reconciliation. Live account validation remains pending. Cross-job duplicate detection is a separate future feature, not part of the current completion guarantee.

## Objective and assumptions

Build `od2gp`, a native CLI for Windows and macOS that copies selected OneDrive photos and videos into Google Photos for personal use. Interpret conversion as migration of original files, without re-encoding. Use personal Microsoft accounts, one source and destination account per job, and an interactive desktop browser for sign-in. Leave source files untouched.

Confirmed scope: personal use, with both photos and videos in version one. MVP: recursive folder selection, JPEG/PNG/HEIC photos and Google-supported video containers (including MP4, MOV, M4V, AVI and MKV), preview, bounded local staging, resumable jobs, progress, and JSON/CSV reports. Inventory all media and explicitly report unsupported formats or oversized files. Validate HEIC and representative H.264/HEVC videos with real fixtures; container support alone does not guarantee successful video processing. Preserve original bytes without automatic transcoding. Defer Live Photo pairing, RAW support, automatic date repair, continuous sync, shared folders, business tenants, and folder-to-album mapping.

## Source access: rclone (revised default)

The user reports approximately 700 GB in OneDrive and cannot obtain an Azure free account. Use rclone as the source adapter, with its supported built-in OneDrive OAuth client, rather than requiring the user to register a Microsoft app. Keep Google Photos uploads and Google OAuth in od2gp. The direct-Microsoft OAuth code is internal legacy code, not a prerequisite. Normal setup and source commands use rclone; the owner reports successful sign-in.

- Install or detect a pinned rclone binary for Windows x64 and macOS arm64/x64. Verify official distribution checksums before executing it; preserve user installations. Source-launcher and packaged-binary setup both need to handle this runtime dependency.
- Guide the owner through creating a dedicated rclone OneDrive remote with blank client ID/secret to use rclone's registered defaults. Request `Files.Read offline_access`; keep `disable_site_permission=false` because setting it to true in the pinned rclone version overrides the custom scope list. Select OneDrive Personal without site discovery. Never copy rclone's client identity into our own OAuth code. Shared-app throttling and consent restrictions may still occur. [OneDrive backend](https://rclone.org/onedrive/).
- Store rclone configuration encrypted, with the configuration password in the OS credential store; supply it via a protected input mechanism supported by rclone, never command arguments or logs. Rclone must be able to persist refreshed tokens. Validate configuration protection and refresh on both platforms.
- Launch rclone as a child process with separate arguments, no shell interpolation. Restrict the adapter to listing, metadata and content reads. Capture bounded/sanitized diagnostics, propagate cancellation and stop orphan workers on restart. Do not expose a remote-control server by default.
- Bind jobs to the actual source drive/account identity and Google account/client identity, not just a mutable remote name. Confirm stable source item/version metadata is exposed by the selected rclone interface. If identity/version proof is unavailable, pause rather than resuming against an assumed source.
- Enumerate via structured rclone output; stream each selected file into the CLI's encrypted staging layer. Investigate `rclone cat --offset/--count` for resuming reads, with source-version validation and exactly one file per request. Do not assume `rclone copy` automatically resumes partial downloads. [Content reads](https://rclone.org/commands/rclone_cat/).

The implemented default staging cap is 30 GiB, configurable when creating a job. It must accommodate the largest eligible file plus encryption overhead, with a further 128 MiB free-disk reserve. Preview warns about insufficient cap; execution refuses a file exceeding it. The complete 700 GB is never staged at once. Cleanup applies only to app-owned temporary media after destination creation has been durably recorded. Copy only eligible photos/videos; 700 GB of total OneDrive data may include unrelated files.

## Destination capacity

The user reports that the destination Google account already has Google AI Pro with a 5 TB storage allowance. Treat this as account-specific supplied information, not a verified general subscription entitlement or 5 TB of free space. Preview totals the selected media bytes but reports destination capacity as unverified. The owner must compare these totals with current free storage manually; an automatic user-supplied capacity comparison remains a follow-up. Do not request extra permissions solely for this check. Existing usage and concurrent changes can affect capacity, so handle storage exhaustion during transfer even after a successful preview.

## Verified API constraints

- Microsoft Graph supports folder enumeration and downloading originals with delegated `Files.Read`. Follow pagination; download URLs are temporary credentials and must not be logged. [Enumeration](https://learn.microsoft.com/en-us/graph/api/driveitem-list-children?view=graph-rest-1.0), [download](https://learn.microsoft.com/en-us/graph/api/driveitem-get-content?view=graph-rest-1.0).
- Google supports uploads with `photoslibrary.appendonly`. Library-wide search is unavailable; read access is limited to app-created content. Therefore, do not promise deduplication against the entire existing library. [API changes](https://developers.google.com/photos/support/updates).
- Upload bytes, then create media items. Batch creation accepts up to 50 entries and should run serially per user. Upload tokens expire after one day. Resumable uploads are supported; photo size limit is 200 MB and video size limit is 20 GB. Google documents identical bytes as resolving to the same media item ID; validate recovery against this behavior. [Upload guide](https://developers.google.com/photos/library/guides/upload-media).
- Default Library API quota is 10,000 requests per project per day, including uploads. Original-quality uploads consume account storage. Estimate requests as well as bytes; resumable chunks and retries increase request consumption. [Quotas](https://developers.google.com/photos/overview/api-limits-quotas).
- Use installed-app OAuth with system browser and loopback callback; PKCE and state validation. Validate offline token refresh and project consent configuration early. [Google desktop OAuth](https://developers.google.com/identity/protocols/oauth2/native-app).
- Use a personally owned Google OAuth app registration and rclone's supported Microsoft OAuth registration for the default source path, with a setup guide for the owner. Public distribution, a public onboarding website and release signing are outside this project scope. Validate consent configuration and refresh-token longevity for multi-day jobs; personal use still requires OAuth setup and applicable data handling. [Policy](https://developers.google.com/photos/support/api-policy).

- Video readiness checks require `photoslibrary.readonly.appcreateddata` in addition to upload permission. Poll saved destination IDs until Google reports `READY`; distinguish pending processing from transfer completion. [Access app-created media](https://developers.google.com/photos/library/guides/access-media-items).
- Persist resumable session URLs and server-confirmed offsets. Sessions expire after seven days; respect server chunk granularity and restart expired sessions explicitly. [Resumable uploads](https://developers.google.com/photos/library/guides/resumable-uploads).

## Implemented technology

Go native executables with the standard-library CLI, `golang.org/x/oauth2`, `modernc.org/sqlite` and `github.com/zalando/go-keyring`; versions are pinned in go.mod/go.sum. Standard-library AES-GCM protects records and staging. Targets are Windows x64 and macOS arm64/x64; Windows arm64 remains a later packaging option.

SQLite stores durable jobs and per-item state. macOS Keychain and Windows Credential Manager store refresh tokens and keys. No plaintext fallback. AES-GCM encrypts sensitive records/session URLs and chunked temporary media, with keys held in the OS credential store. Bounded chunk reads and corruption refusal are tested; native storage/runtime acceptance remains open. Restrict file permissions and remove staged content after confirmed creation.

Use platform application-data/cache directories, rclone as a verified runtime dependency (the macOS keyring adapter also uses the built-in security utility via stdin), generated staging names rather than source filenames, and Unicode-safe paths. This avoids Windows reserved names and long-path issues.

## Platform policy requirements

The [policy review](../docs/platform-policy-review.md) found no documented prohibition on owner-authorized personal migration. Before live account access, implement in-flow disclosures, affirmative consent, a privacy notice, secure data handling, account display, disconnect/local cleanup and provider quota handling. These are acceptance gates, not a claim of platform approval.

## First-run toolchain setup

Confirmed requirement: running from source should automatically check for Go and install it when missing. Provide a macOS shell launcher (`scripts/od2gp.sh`) and Windows PowerShell launcher (`scripts/od2gp.ps1`) so this can run before any Go code is compiled. Packaged native binaries run directly and do not require the Go compiler.

1. Detect the supported operating system and architecture, then check Go on PATH and the launcher-managed installation. Verify the executable works and satisfies the project toolchain requirement, rather than only checking that a file exists.
2. Reuse a compatible installation. If Go is missing, broken or too old, automatically download the project's pinned supported Go release for this OS/architecture from the official Go distribution service over HTTPS. Maintain version, archive filenames and expected SHA-256 checksums in a reviewed toolchain manifest (`scripts/toolchain.tsv`) derived from [official Go downloads](https://go.dev/dl/?mode=json). Do not execute downloaded installation scripts.
3. Verify the checksum before extraction. Extract into a fresh temporary directory and promote only a validated installation; never unpack over an existing Go tree. Install under the launcher's private user-owned tool directory, with an override for a writable location. No administrator access or global PATH/profile changes are required. Preserve an existing system Go installation.
4. Run the installed `go version`, use the selected executable explicitly to build the CLI, and launch it with the original arguments and exit code. Align the pinned bootstrap release with CI and the module's minimum Go version. Limit PATH changes to the launcher and its child processes.
5. Reuse the managed toolchain on later runs. If downloading fails, storage is insufficient, checksums disagree or the platform is unsupported, exit with a clear error before login or transfer. Remove only owned temporary downloads, preserve any working installation, and allow a subsequent retry. Serialize concurrent installations to avoid corrupting the cache.

The `od2gp setup` wizard now opens only Google Cloud and saves the Google JSON path. `auth login onedrive` installs/detects rclone and configures the dedicated encrypted remote. Microsoft client-ID entry is no longer part of the normal setup. Environment overrides remain available. The source launcher may rely on standard platform tools (shell/curl/tar/checksum utilities on macOS and PowerShell download/archive/hash facilities on Windows); check those prerequisites explicitly. The compiled CLI retains its standalone runtime behavior. Test paths with spaces and a clean machine without Go. An offline first run without Go cannot install it; report the network requirement without claiming setup succeeded.

Status: source launchers are implemented with ten passing offline macOS orchestration tests. The owner installed Go and ran the CLI on macOS. Agent-side Go tests/builds pass using the cached toolchain; agent-side first-run downloads remain network-restricted. Native Windows startup and clean-machine installation are still acceptance gates; cross-builds do not establish runtime compatibility.

## Capability map

| Module | Responsibility | Depends on |
|---|---|---|
| identity | Browser authorization, account binding, secure tokens | — |
| journal | Durable state, encryption, job locking | — |
| onedrive | Invoke rclone for source login, enumeration and original-byte reads | identity |
| google-photos | Upload and create destination media | identity |
| migration | Plan and execute bounded, recoverable transfers | journal, onedrive, google-photos |
| cli | Commands, progress, reports, cancellation | migration |

The end-to-end path and recovery/retry slices exist. The next work proves them against real media and native environments before scaling and packaging.

## Account identity and long-running sessions

Request OpenID identity scopes alongside provider access scopes to bind jobs to stable account identifiers. Validate the identity through a trusted provider endpoint or a maintained OIDC library; do not trust an unverified JWT payload or a user-entered email as proof. Account identity and OAuth client identity must match before resume. A changed client registration can change app-created visibility.

Google external OAuth apps in Testing generally receive refresh tokens expiring after seven days for the required Photos scopes. Preserve progress and prompt for browser reauthorization on invalid_grant, rather than repeatedly retrying or discarding state. Do not assume personal use or paid storage removes this restriction. [Google OAuth token lifetimes](https://developers.google.com/identity/protocols/oauth2/).

No sleep-prevention feature is promised in version one: document that the CLI and computer must remain awake and online, and test cancellation and restart. Cross-compilation is not proof of runtime compatibility; native OS tests and credential-store smoke tests remain separate acceptance gates.

## Data flow and recovery

OneDrive via rclone → SQLite manifest → bounded download/staging → SHA-256 → Google upload → media creation → record destination ID → clean staging.

Key records by source account, drive ID, item ID, source version and destination account. Preserve a content hash to recognize identical source content. Track discovery time, original path, MIME type, size, version, state, retries, error category and destination media ID. Keep credentials out of reports and logs.

States: discovered → downloading → staged → uploading → uploaded → creating → created. Photos then become completed; videos pass through processing before completed. Only mark a video completed when its processing status is READY. Also represent skipped, retryable failure, permanent failure, and uncertain creation outcome. Record intent before network mutations and completion transactionally afterward. Keep upload sessions/token expiry in protected state. Lock active jobs to prevent simultaneous competing runs.

On restart, check each durable state. Query resumable-upload status rather than guessing offsets. Refresh expired download URLs, re-upload expired tokens, and retry failed batch entries individually. After a lost creation response, reconcile using documented same-bytes behavior and an explicitly tested recovery procedure; do not claim exactly-once execution solely because a local database exists. Surface unresolved outcomes in reports. Bind resume to the original accounts and OAuth application identity.

Poll video status with increasing intervals and a bounded per-run wait; persist pending work for `resume` rather than re-uploading it. Report processing failures distinctly and never retry them as new uploads automatically. Staging can be cleaned once creation and its media ID are durably recorded, including while processing is pending; OneDrive remains the recovery source.

Use resumable chunk uploads for videos and bounded read buffers rather than loading an entire video into memory. The MVP stages each file fully before upload: the configured cache must fit the largest selected file plus encryption overhead. Preview identifies insufficient cache capacity with an actionable message, never silently exceeding the cap or deadlocking the queue. Validate range-download recovery and source-version consistency; if range resumption is unavailable, restart the download explicitly.

Check source versions before/after downloading so edits during transfer cannot silently mix content. Re-plan changed items. The current executor uses a configurable worker pool with one shared staging budget, free-space reservations and Google backoff. Respect Retry-After and use exponential backoff with jitter. Pause cleanly on exhausted quota, revoked consent, or destination storage failure.

Preserve embedded photo EXIF and video container metadata by transferring original bytes. OneDrive filesystem timestamps are not necessarily capture dates. Record missing metadata and verify Google-displayed dates/orientation with sample photos and videos (including playback, audio and rotation); do not silently rewrite originals. API success is evidence of creation, not byte-for-byte destination archive verification.

## Shutdown and power-loss recovery (required)

Goal: restarting the app continues the saved job after an unexpected shutdown. Resume is from durable checkpoints, not a promise to preserve bytes that never reached disk or the provider. No shutdown callback can be relied on during power loss.

- Store the job journal in a persistent local application-data directory, separate from disposable build/cache output. Use SQLite transactions, WAL and `synchronous=FULL`; verify applied settings. On macOS evaluate `fullfsync` for the platform's stronger flush semantics. Acknowledge filesystem/hardware flush limits; durability cannot protect a failed disk. [SQLite durability settings](https://sqlite.org/pragma.html#pragma_synchronous).
- Flush encrypted staging chunks and their authentication metadata before committing a matching download offset to the journal. On restart, verify chunk authentication, source version and file length; discard or truncate only uncommitted/torn app-owned data. Resume from the last verified boundary where supported; otherwise restart only the interrupted file's download.
- Persist upload-session URL, expiry, item state and completed media IDs in protected storage. After restart, query Google's accepted byte offset and use its answer as authoritative; do not assume a saved local offset equals bytes accepted remotely. Expired sessions/tokens require a new upload for that unfinished item. [Google resumable uploads](https://developers.google.com/photos/library/guides/resumable-uploads).
- Record creation intent before `batchCreate`, then durably save each returned destination ID/status before cleaning staging. A crash after remote creation but before the local commit produces an uncertain outcome: reconcile with app-created visibility/documented same-bytes behavior. If unresolved, retain an explicit uncertain state rather than marking success or blindly uploading duplicates.
- Skip confirmed completed items. For created videos, resume status polling using saved IDs until READY; never re-upload merely because processing was interrupted locally.
- Acquire an OS-held job lock released when the process dies; account for surviving rclone children. On startup recover SQLite, verify account/drive bindings, validate staging and reconcile remote state. A corrupt journal stops the job with a recovery message; never silently create a new job in its place.
- If an incomplete job exists, show its progress and offer to resume as the default choice after reopening the CLI. Also provide `od2gp resume --job JOB_ID` for explicit restart. Default version one requires reopening the CLI; automatic launch at OS boot/login is optional future work and is not enabled implicitly.
- Reauthorization may be needed after a long outage or revoked consent. Keep all progress while requesting it. Resume depends on the same journal, credential-store keys and original source availability remaining accessible.

Acceptance: abruptly kill the process at each download/upload/create/commit/cleanup boundary, including immediately after acknowledged destination creation. Restart the same job and reconcile all items without silent omissions, false completion, duplicate work on confirmed items or unbounded cache growth. Test torn cache chunks, expired sessions, stale locks, pending videos and source edits. Supplement process-kill tests with a disposable VM hard-power-off test; process termination alone does not validate storage flush guarantees.

## Current commands

```text
od2gp menu
od2gp setup
od2gp auth login onedrive
od2gp auth login google
od2gp auth check onedrive
od2gp auth check google
od2gp source select-drive
od2gp plan --source /Pictures --cache-gib 30
od2gp run --job JOB_ID --wait --retries 5
od2gp status --job JOB_ID
od2gp resume --job JOB_ID --wait --retries 5
od2gp report --job JOB_ID --format csv
od2gp auth logout google
od2gp auth logout onedrive
```

The interface uses --job JOB_ID with an encrypted persistent journal. Bounded parallel transfers and sequential creation batches of up to 50 items are implemented; live throughput remains to be measured. `plan` uploads nothing and prints counts, eligible bytes, completion/skipped/processing totals and staging requirements; destination free capacity is marked unverified. `run` requests confirmation and displays the Google account. Detailed rows are available through JSONL/CSV reports; estimates and richer preview identity summaries remain follow-ups. Ctrl+C preserves committed progress. Exit codes distinguish completion, error/interruption, invalid usage, pending video processing and processing failures.

`menu` provides numbered connection/setup actions, a drive picker before folder/cache prompts, preview followed by upload confirmation, saved-job selection, status and private CSV/JSONL export without overwriting existing files. Option 7 and `source select-drive` reopen drive selection even for a working connection. Selection reuses the stored sign-in, retains read-only scopes, validates a temporary encrypted config and shows folders plus sample photo/video filenames before explicit acceptance and publication. Each metadata-only inspection is bounded to 20 folders from 200 entries, 10 media examples from 2,000 checked filenames and 15 seconds; the user can inspect a known folder, choose another drive or cancel. Missing examples do not prove absence of media; cancellation/failure leaves the active config unchanged. Source locks prevent changes during an active transfer. Existing jobs retain their original drive binding and refuse resume against a different drive. The menu defaults to continuing when unfinished jobs exist and identifies saved jobs by folder, counts and date. No-argument startup opens it only when both input/output are terminals; redirected usage keeps the original help/list behavior. Automatic OS boot/login launch is not enabled.

No hosted relay is required. Media bytes pass through the user's computer, so migration consumes both download and upload bandwidth and runs while the computer is awake and online.

## Delivery sequence and acceptance gates

0. First-run setup: implement and verify the source launchers on clean Windows/macOS environments with Go absent, compatible and outdated.
1. Feasibility: use the working account setup to migrate a mixed set of representative photos and videos, including a multi-gigabyte video on both OS families. Verify refresh, dates, orientation, video playback/audio, same-bytes replay, and secure staging/keyring access before committing to bulk transfer. Scanner implementation is already available; its presence does not waive this live validation gate.
2. Preview: recursively enumerate a selected folder with pagination, stable IDs and resumable discovery. Produce an accurate manifest without uploading.
3. Durable migration: bounded transfer queue and checkpointed state, photo completion after destination creation and video completion after READY, correct per-item reporting.
4. Recovery: inject failures at every state boundary, lost API responses, token expiry, source edits, disk full, quota exhaustion, interrupted video chunks and delayed/failed video processing. Restart successfully with no silent omissions or false completion.
5. Release: test actual packaged binaries on Windows and macOS, produce local binaries/checksums and personal OAuth setup docs. Public release publishing and signing are outside scope.

Developer checks: `go test ./...`, `go test -race ./...` where supported, `go vet ./...`, `go build ./cmd/od2gp`. Use simulated HTTP transports for deterministic failure tests, opt-in real account smoke tests, and CI build/test jobs on both OS families. A synthetic 10,000-file manifest must stay within the configured staging cap and reconcile every item as completed, processing, skipped or failed; the job remains incomplete while processing items exist. Do not require 10,000 real uploads merely to validate scaling.

## Remaining inputs and later work

Measure eligible bytes, actual download/upload throughput and provider pauses during the trial before estimating the bulk migration. The CLI currently downloads a file before uploading it, so both directions consume time; the 700 GB source size alone is not enough to estimate completion.

Confirmed: personal purpose; both photos and videos are required; source setup uses personal OneDrive; destination has a user-reported 5 TB allowance under Google AI Pro. Remaining sizing inputs are current available destination storage, eligible media count/bytes within the reported 700 GB, largest video and available local disk. Album mapping remains optional.

Optional next release: flatten source folder paths into app-created album names, handle name collisions and album limits, then add delta-based incremental import. Continuous synchronization and deletion propagation need a separate design.

## Initial transfer implementation and remaining gates

The first engine stores encrypted job/item records in SQLite with WAL/FULL durability and macOS fullfsync, stages bounded concurrent files using authenticated encryption, validates source identity/version, persists upload sessions and queries accepted byte offsets on resume. Completed items are skipped; videos remain pending until READY. Missing staged data triggers a safe re-download of that file. Interrupted discovery restarts its pre-transfer scan. Status without a job lists saved IDs, including after reopening the CLI.

Uncertain creation outcomes stop normal resume. `resume --reconcile` explicitly retries the hash-verified identical staged bytes under Google's documented same-bytes behavior; actual provider behavior remains a live validation gate. File bytes transfer in parallel and checkpoint each upload chunk; media creation uses sequential batches of up to 50 items. Recoverable Google HTTP 429/5xx and recognized network errors receive bounded exponential retries with jitter, five per file by default (`--retries 0-10`). Retry-After pauses are persisted and enforced on resume; waits over 15 minutes stop for a later explicit resume. Uncertain creation outcomes, authentication/permission errors and unclassified failures are not automatically retried. Video readiness is checked once per run by default; optional `run --wait` / `resume --wait` polls with increasing delays for up to 30 minutes after the initial transfer pass. Interactive startup now offers the menu, defaulting to Continue when unfinished readable jobs exist; numbered selection avoids copying IDs. Automatic OS boot launch is not implemented.

Tests cover database reopen, lost upload responses, lost creation responses, explicit reconciliation, missing staging, source changes, encrypted data corruption and pending videos. This is not a substitute for native Windows runtime checks, actual Google Photos upload/playback verification or a VM hard-power-off test. See docs/migration.md for the exact current commands and limits.


## Native Graph source increment — implemented, live verification pending

Configure the owner's public application ID `dd4ceec5-81a2-4c43-b4f7-5680436926aa` as the personal-project default (environment overrides remain available). New jobs use a native Microsoft Graph source; old jobs without a backend field remain rclone jobs and must never be silently converted because their version metadata and authentication bindings differ. Keep rclone as an explicit fallback through `OD2GP_SOURCE_BACKEND=rclone`.

Implement browser PKCE login with personal-account authority, read-only Files.Read and offline access, OS-held refresh credentials, validated account identity and refresh-token rotation. Add native paginated children inventory, drive selection/content preview, source version checks and streaming downloads through the existing encrypted staging and durable transfer engine. Authenticate only Graph API requests; preauthenticated content redirects use a separate anonymous HTTPS client. Reject untrusted pagination URLs and unsafe paths. Preserve explicit Vault exclusions and fail rather than silently omit unsupported remote shortcuts.

Acceptance: simulated Graph pagination, paths, errors, account/client binding, download redirect credential isolation, version checks and old-job backend compatibility pass; native builds pass. Owner performs real native login/scan and a non-overlapping test transfer, then measures per-stage performance. This increment removes rclone subprocesses from native transfers; concurrent uploads now share a staging budget; Google creation batching is now implemented. No throughput guarantee is made before a live benchmark.


## Guided Personal Vault handling and overlap warning — implemented

Observed: native root job `54f77861b717b5e67baa2b15312eb09b` was saved without the Vault exclusion. Continue restarts discovery, encounters Vault and returns to the menu, leaving the operator to supply a CLI flag. The menu now offers the explicit exclusion during creation and recovery.

- [x] For a new `/` plan, offer an explicit choice to exclude `/Personal Vault` and explain that its contents/count/size will remain unknown and uncopied. Keep cancellation available; do not silently enable exclusion.
- [x] For an incomplete root job without that policy, offer the same choice during Continue and apply it to the existing job ID, preserving accounts, backend, drive, staging cap and progress. Declining or cancelling must not change the saved policy. Completed inventories must retain their fixed scope.
- [x] Display the saved exclusion before upload approval and retain it in status/reports. Verify new-plan acceptance/decline, existing-job acceptance/cancel, persistence on another continuation, and no prompt for non-root/already-excluded/completed jobs.

Implemented safeguard: warn before a new or unstarted job uploads when another saved job may overlap. The latest owner output shows 13 completed files in legacy root job `84f8505d28b7ff00c8aab99f1c69533d`; the new native root job has separate history. A warning must explain duplicate risk without claiming cross-job deduplication exists. Decide the live trial scope before further overlapping uploads.

Files likely touched: `internal/cli/menu.go`, `internal/cli/menu_jobs.go`, corresponding menu tests, and operating instructions. Depends on the implemented explicit Vault flag and saved metadata policy. Exit criterion: an operator can resolve this root-job Vault failure entirely through the menu with an explicit scope decision, and no new job or upload starts from a cancellation.

Automated menu tests cover explicit include/exclude/cancel choices, preserving the existing job ID, fixed scopes, overlap boundaries and bindings, and refusing uploads after rejected overlap consent. Live owner verification of the menu and transfer remains pending.

## Parallel file transfers — implemented, live benchmark pending

Owner confirmed native root inventory and photo uploads work, but per-file serial latency is too high. Implement a bounded worker pool (CLI/menu default 3, `--workers 1-8`) using the existing job and per-item checkpoints. Upload bytes concurrently and serialize library creation per job, following Google's upload guidance. Keep existing account/source locking; run only one transfer job per destination account at a time. Reserve encrypted staging sizes under one shared cap, count prior-process staging, reserve disk space for pending writes, and preserve retry pauses across workers. Stop dispatch on the first unrecoverable error, cancel and join workers, then return with durable item states. Refuse automatic retries of uncertain creation.

Acceptance: deterministic overlap, worker/staging bounds, sequential creation, completed-item skipping, interrupted parallel-to-serial recovery, active cancellation, unknown staging and shared Google retry-pause tests pass under the race detector. CLI rejects unsupported worker counts before touching jobs. Run full tests, vet and cross-builds. Owner restarts the same job and measures throughput; representative photo/video and native Windows/power-loss acceptance remain open. Creation batching was added in the following increment; concurrent inventory remains outside scope.

## 2026-10-19: durable sequential batch committer

Preserve the existing Go/HTTP/OAuth/OS-keyring stack and encrypted journal/staging. Separate concurrent byte upload completion from sequential `batchCreate`. Save tokens before queueing, prepare each creation batch atomically, and persist known per-item outcomes before cleanup. Default maximum 50 (`--batch-size 1-50`), with a two-second partial-batch timer, end-of-upload flush and staging-pressure flush. Count queued files under the shared staging cap; workers do not wait for creation to receive another upload. No database replacement or scope expansion is needed. Handle missing/mismatched responses as uncertain, and retry only explicit temporary failures. Display safe HTTP operation/status diagnostics and batch duration. Keep prior jobs and owner live evidence intact.

Acceptance: batch size/partial tail, timer/pressure/end flush, atomic encrypted preparation/outcomes, partial results, temporary rejection subsets, HTTP 429 pauses, ambiguous-response refusal/reconciliation, and old-job resume under the race detector. Run full tests, vet, bootstrap tests and Windows/macOS builds. Owner must benchmark actual batches and verify video READY/playback/audio; Windows runtime and hard-power-off remain pending.

## Remembered menu settings and desktop entry points — 2026-10-19

Move normal tuning into menu option 8. Offer Recommended (3/50/5/wait), Gentle (1 worker), Faster (6 workers), and Custom (workers 1-8, batch 1-50, retries 0-10, wait yes/no). Speed-only presets preserve other choices. Store validated non-secret menu preferences separately from OAuth configuration and job journals, using private atomic replacement. Reuse them for new and continued jobs without extra prompts on each run. Keep job scope/account/staging bindings fixed.

Count journal `creating` items in menu jobs and label recovery needs. Continue offers status review, explicit original-byte reconciliation or Back; never persist reconciliation consent. Add double-click macOS `.command` and Windows `.cmd` launchers that open the existing source-built menu without bypassing OS script policy. Validate settings persistence across application launches, invalid input, save failure/cancellation, preserved OAuth settings, exact transfer flags, review/recovery consent and no consent reuse. Native Windows desktop behavior and cloud recovery remain live checks.

## Menu language selection — 2026-10-19

Add option 9 for English / Tiếng Việt, saving `en` or `vi` in the existing non-secret menu preferences. Missing language remains English for backward compatibility. Validate supported languages, retain prior values on cancellation/save failure, and preserve transfer preferences during language changes and language during preset changes. Translate only explicit menu UI templates before formatting. Never run report/file data, IDs, paths, command arguments or provider responses through a text-replacement translator. Core menu subpages are bilingual; detailed/delegated CLI output remains outside this increment. Test fresh-launch persistence, switching back, recovery/settings behavior, format-parameter compatibility and unchanged command/report protocols, then commit and attempt a verified private-repository push.


## Current increment: source retries, filters and recovery verification (2026-10-19)

- Graph GET requests retry HTTP 429/502/503/504 at most three times, with exponential jitter and Retry-After seconds/date support. Each attempt is bounded to 30 seconds. Retry waits are cancellable; pauses longer than 15 minutes stop automatically. Exhaustion/cancelled waits expose a safe deadline that active job discovery/transfer saves in the encrypted journal. Read-only scans without a saved job show the deadline but have no durable job checkpoint.
- Retries happen before handing a response body to a caller. Partial download streams and staging-write errors are not replayed into an existing writer. Authentication/permission errors stop immediately. Source GET retries are separate from the menu's Google transfer retry count.
- Menu option 10 remembers literal drive-root folder filters for new migrations. Repeated `plan --exclude-folder /Documents/Projects` works in the CLI. Policy is saved in the job and shown in preview/reports; Continue preserves it. Filters are case insensitive at folder boundaries, support Unicode and reject traversal. Full inventory is retained, with matching files explicitly skipped; these filters do not speed up scanning or bypass inaccessible folders. Personal Vault still requires its separate explicit choice.
- Local reopen tests cover partial batch successes plus unknown outcomes, explicit reconciliation of only uncertain items, pending-video polling by stored ID, and durable Graph pause recovery. They do not substitute for abrupt-kill, live provider or hardware power-off acceptance.

Next gates: owner validates a small non-overlapping filtered plan and continued existing-job throughput; verify representative video READY/playback/audio, then native Windows and disposable-VM power-loss recovery before the 700 GB run. Existing jobs retain their current source scope and staging cap.


## Large-download and saved-cap increment — 2026-10-26

Implemented idle-based downloads, authenticated source-range resume, bounded source retries coordinated through the job's provider pause, and increase-only saved-job staging resize. These supersede the earlier full-download restart and immutable-cap limitations. Continue → c performs a local locked metadata update with no uploads; subsequent Continue uses the same history. Remaining gates are live throughput, representative video READY/playback/audio, native Windows credentials, abrupt-kill and disposable-VM power-loss acceptance.


## 2026-11-02 — Planned feature: OneDrive → Google Drive

Status: implementation authorized and added on 2026-11-02. Local automated tests cover the Drive client, account isolation, folder-aware inventory, durable IDs, resumable transfer, retry beyond the old budget, final tree reconciliation and Auto-workers. Source and destination account/cloud trials, native desktop consent and actual power-loss acceptance remain pending on the other machine designated by the owner. Existing Google Photos jobs and historical acceptance records remain intact.

### Agreed scope and completion contract

- One-way, read-only-source copy of all ordinary files and the complete folder tree from one selected OneDrive drive or folder into a dedicated Google Drive destination folder.
- Preserve exact file/folder names, Unicode and parent-child relationships. Preserve empty folders, including folders whose only children are excluded zero-byte files. Never silently rename or flatten paths.
- Skip only files whose size is confirmed to be exactly zero; report `skipped_empty_file`. Unknown size or failed metadata requests are errors to resolve, not empty files.
- Preserve original binary contents and extensions; do not convert Office files to Google Workspace formats. Preserve source timestamps where supported and report metadata differences.
- Keep identical files at separate source locations as separate destination entries. Do not merge by filename or content alone. Keep OneDrive originals and pre-existing destination data.
- Permissions, share links, version history and service-specific objects are not automatically portable. Personal Vault, inaccessible objects, OneNote packages and remote/shared shortcuts must be classified explicitly. Unsupported or inaccessible content prevents a claim of full coverage unless the user accepts a persisted, visible exclusion. Unknown contents retain unknown counts/bytes.
- Success means all in-scope folders and nonempty ordinary files have verified destination records, with no unresolved items. Reports distinguish completed, empty-file exclusions, accepted exclusions, retrying, intervention-required and unverified outcomes.
- This is a migration, not continuous two-way synchronization. Source changes during transfer require version validation and a final reconciliation pass. Exact final equivalence requires a stable source during that pass; no atomic whole-drive snapshot is promised.

### Guided account linking (first implementation slice)

Add bilingual menu entries for Link OneDrive, Link Google Drive, Check both connections and Reconnect. Each step offers Open link, Copy required value, Done/check and Back; display the URL as a fallback when browser/clipboard integration is unavailable. Persist non-secret setup progress. Import existing client configuration or guide creation; no environment-variable editing required in normal use.

Microsoft: open https://entra.microsoft.com/ and guide App registrations, account types supporting personal Microsoft accounts, Mobile/Desktop platform and the exact redirect URI used by the CLI. Use delegated read-only `Files.Read` for ordinary own-drive access plus required identity/offline scopes. Do not request write access to OneDrive. Validate client configuration, authenticate, show account identity, list drives and verify source reading. Tenant/access errors must have specific guidance.

Google: select a project, open https://console.cloud.google.com/apis/library/drive.googleapis.com to enable Drive API, and guide OAuth consent and Desktop OAuth client creation at https://console.cloud.google.com/apis/credentials. Accept a JSON file using a file picker where available or a path prompt. Prefer `drive.file` for application-created/explicitly authorized destination content; an arbitrary pasted folder ID does not grant access. Existing-folder selection needs an authorization mechanism such as Picker, or is excluded from the first slice in favor of an app-created destination root.

Validate API availability, granted scopes, destination identity and available storage. Do not mark linked merely because JSON is structurally valid. Signing in does not transfer content. Confirm destination creation separately. Distinguish disabled API, missing test user, denied scopes and redirect mismatch. Explain Google External/Testing refresh-token expiry (normally seven days for Drive scopes); guide appropriate publishing configuration without promising permanent credentials. Console publishing state may require user confirmation if it cannot be read with the app's limited permissions.

Keep credentials and encrypted refresh/session information local, protected with the OS credential store. Bind jobs to source/destination identities and OAuth clients. Reconnection must not silently switch a saved job to a different account.

### Inventory and destination structure

Retain both folder and file nodes with source drive/item/parent IDs, exact names, type, size and source version. Reuse resumable Graph inventory where suitable; do not reuse Photos-only media classification. Check storage, largest file, staging capacity, hierarchy depth and provider item limits before transfer. Every discovered object must have a recorded classification.

Persist the mapping `(source account, drive ID, item ID, source version, destination account, destination root)` to Google Drive item and parent IDs. Create parents before children; independent branches may proceed concurrently. Local staging uses internal IDs rather than original paths so Windows filesystem restrictions do not rename cloud objects. Existing destination conflicts require explicit resolution; no automatic overwrite, merging or suffix renaming.

### Transfer and durable recovery

Add a Google Drive destination client and a dedicated tree-transfer engine. Share OAuth/keyring, encrypted journal/staging, read-only Graph transport and UI infrastructure where safe; do not route Drive through Photos upload tokens or batchCreate.

Pre-generate supported Drive IDs and save them durably before file/folder creation. On timeout, conflict or ambiguous result, query the same destination ID and reconcile identity/content before retrying. Never create a new destination ID solely because the response was lost.

Use bounded concurrent downloads/uploads and resumable upload sessions for large files. Persist session, offsets, source version and destination IDs. Query server progress on resume. Expired sessions require a destination check before restarting upload with the same identity. Save verification durably before deleting staging; cleanup must also recover after a crash. Keep journal history after cache deletion.

Fix and regression-test the previously identified cross-job identity/metadata matching and skipped-partial-cache issues before reuse. Drive jobs must not inherit Photos completion history. Cross-job reuse requires exact account/root bindings and sufficient source-version evidence; missing facts must not establish a match.

### Unattended operation — agreed behavior

Do not terminate the whole Drive job because a transient operation exceeded five attempts. Use bounded retry bursts followed by a persisted cooldown and automatic requeue. Bound request frequency and maximum backoff, not the total number of transient-recovery cycles. Honor Retry-After and provider quota signals; do not busy-loop or evade limits.

A repeatedly failing file moves to a delayed queue while other files continue. A failed folder blocks only its dependent subtree. Global network/provider pauses suspend dispatch appropriately; refresh credentials automatically when permitted. Staging pressure applies backpressure and waits for cleanup rather than starting more downloads. Surface retained-cache blockers when no work can release space.

When all remaining work is delayed, stay alive with a visible reason and next retry time. Authentication requiring renewed consent, persistent access denial, exhausted storage, invalid source/destination state or local disk failure enters an explicit intervention state. Keep processing independent eligible work where possible. A source failure, machine sleep/shutdown or user cancellation can also interrupt work; destination-server errors are not the only physically possible blocker. Durable recovery continues after relaunch; boot auto-start is not promised.

### Integrity and reports

Check destination ID, exact name, parent, size and source version; reconcile all folders including empty ones. Compute SHA-256 during download and compare with destination checksum when supplied. If checksum is unavailable, label the actual verification strength; do not claim byte-level verification based only on names/sizes. Any additional full download for verification must be an explicit mode with its bandwidth cost shown.

Recheck source version around transfer and classify changes for another scan/retry. Final verification reports missing, unexpected, changed, pending and unresolved nodes; never hide these beneath a success status. Export CSV/JSON with source-relative path, target ID, status, verification and reason; redact secrets.

### Stable Auto-workers — release gate

Default start: 3 workers; adjustable range 1–8 with fixed-worker fallback in menu. Measure transfer throughput, latency, retry/throttling rate and staging pressure over time windows, not just successful-file counts. Mixed small files and large videos must not falsely indicate an improvement.

Increase by one only after a stable observation window; enforce cooldown/hysteresis. Compare throughput before/after and revert an increase without meaningful benefit. Reduce on throttling, recurring network errors or staging pressure. Reducing concurrency stops new dispatch without canceling healthy in-flight uploads. Share provider pause state across workers. Keep memory, pending requests and staging within hard bounds independent of the current worker count.

Display active/target workers and adjustment reason, file/folder counts, transferred bytes, speeds, approximate ETA, staging usage and retry countdown. No per-chunk scrolling in normal mode; retain diagnostics mode. Choose numerical thresholds from controlled mixed-workload tests before enabling automatic mode by default.

### Ordered delivery and acceptance

1. Account wizard and destination identity: link both accounts on Windows/macOS; correct error routing, secure storage and no upload on login.
2. Minimal vertical slice: new Drive job, one nested folder, one nonempty file, empty folder and zero-byte exclusion; verify exact tree, content and IDs.
3. Full inventory/preflight: checkpoint folder/file scans, classify special objects and preview totals/limits without silent exclusions.
4. Durable tree scheduler: parent dependencies, account/job locking, pre-generated IDs and explicit conflicts; repeat requests do not duplicate entries.
5. Bounded transfer/cache: parallel transfers, large-file resume and durable cleanup; preserve cap and source-version checks.
6. Unattended recovery: network/DNS/429/5xx beyond the old retry budget, failed subtree isolation and automatic recovery after service returns; no false completion.
7. Integrity and final reconciliation: tree, sizes/checksums, changing source and complete reports.
8. Auto-workers: measurement-driven tuning, cooldown and staging bounds under fault injection and mixed file sizes; retain fixed mode.
9. Cross-platform acceptance: full tests/race/vet/build and Windows PowerShell/pwsh/macOS menu tests; live small trial, medium trial, interrupted/power-loss recovery, then full-drive migration.

No large live migration acceptance claim based solely on mocks or CI. Gates require no duplicate destination creation, no missing folders, no unreported omission, preserved cache bounds and correct restart behavior.

References (checked 2026-11-02):
- https://developers.google.com/workspace/drive/api/guides/folder
- https://developers.google.com/workspace/drive/api/guides/create-file
- https://developers.google.com/workspace/drive/api/guides/manage-uploads
- https://developers.google.com/workspace/drive/api/guides/api-specific-auth
- https://developers.google.com/workspace/drive/api/guides/limits
- https://developers.google.com/workspace/drive/api/reference/rest/v3/files
- https://developers.google.com/identity/protocols/oauth2#expiration
- https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-register-app
- https://learn.microsoft.com/en-us/graph/api/resources/package?view=graph-rest-1.0


### Google Drive implementation evidence — 2026-11-02

- New `internal/googledrive` client, isolated `gdrive` OAuth/keyring entry, `internal/drivemigration` journal/model/scheduler, root Graph delta checkpoint with folder emission and native Graph folder traversal.
- Main menu 11 opens the bilingual Drive menu. Guided setup persists console steps and imports Desktop JSON; actual identity/Drive checks are separate from user-acknowledged console configuration.
- Dedicated `drive-jobs` journals cannot be mistaken for Photos jobs. Source/destination clients/accounts, saved source drive, destination root parent ID and target IDs are bound before mutation.
- Fault tests cover lost creation/final-upload responses, retries beyond five failures, expired sessions, reopened journals, cache cleanup, independent work after a folder failure, one-file staging with eight workers, source/destination tree changes, and tuner cooldown/rollback. Reports omit resumable session URLs.
- The legacy Photos cross-job index now requires source identity, destination media ID and version metadata; missing drive/subject bindings cannot match. Skipped partial staging is removed after the saved skip decision and on later resumes.
- Auto-workers defaults to auto for Drive only; starts at 3, stays within 1–8, measures acknowledged chunk bytes/request latency over 20-second windows and backs off under errors or staging pressure. Photos tuning remains separate. Numerical thresholds have synthetic/fault-test evidence, not a live throughput-optimality claim.
- Root inventory resumes saved delta pages. Selected-folder inventory restarts an incomplete scan, without starting uploads until complete.
- Final verification checks app-accessible destination membership and source tree freshness. `sha256` and `metadata_only` are reported distinctly; the latter is not a byte-identity claim. A file modified/moved at the source requires explicit resolution/replanning rather than silently changing the saved migration.
- Known first-release scope: app-created destination root in personal My Drive; no arbitrary existing-folder Picker, Shared Drive destination, OneNote export, permission/history migration, continuous sync, or boot auto-start. Inaccessible/unsupported source content blocks inventory; only Vault has an explicit exclusion UI.
- Owner stated live trials will execute on a different machine. Automated Gate C is satisfied by CI. Keep Gate A, GD12, fresh-machine walkthrough and full-rollout approval open until that machine supplies results; CI is not a substitute for actual OAuth/large-transfer/power-loss validation.


Automated checkpoint evidence: GitHub Actions [41421783521](https://github.com/tsunjinx/onedrive-to-gphotos/actions/runs/41421783521) passed all nine Windows/macOS jobs on a6f2be8. Final refinements add non-scrolling Drive worker/speed/cache/wait metrics and account/size preflight summaries; the subsequent push reruns CI. Live acceptance remains assigned to the other machine.
