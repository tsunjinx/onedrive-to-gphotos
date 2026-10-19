# Current work and acceptance checklist

Updated 2026-10-19. See [the current plan](plan.md), [operating instructions](../docs/runbook.md) and [validation evidence](review.md). The checklist below separates implementation from live acceptance.

- [x] Source launchers, rclone setup and Google authentication implemented; owner reports both account checks working.
- [x] Metadata inventory, encrypted SQLite/staging, plan/run/resume/status/report implemented.
- [x] Google resumable uploads, video polling and bounded retries implemented.
- [x] Interactive menu with connection checks, guided preview/start, numbered saved jobs, progress/report export and account setup implemented.
- [x] Remembered menu speed/custom options, explicit guided recovery and desktop menu launchers; no worker/batch/reconciliation flags needed for normal interactive use.
- [x] English/Tiếng Việt selection in option 9, remembered across launches without changing transfer preferences or command/report protocols.
- [x] Metadata-only drive contents preview before saving selection, with folder inspection, another-drive and cancellation choices.
- [x] Explicit OneDrive drive picker in menu option 7, account submenu and new-migration flow; checked encrypted configuration replacement and original-job drive binding preserved.
- [x] Automated race tests, vet and Windows/macOS cross-builds pass.
- [x] Document same-job upload tracking, independent new-job history and explicit uncertain-creation reconciliation.
- [x] Surface drive-selection protocol errors, bound selection requests and improve inventory decoding diagnostics; incomplete scans finish before upload confirmation.
- [x] Identify Personal Vault as the root-scan blocker; add explicit, persisted exclusion with visible summary/report disclosure and no silent omissions.
- [x] Legacy test inventory completed with explicit Vault exclusion; owner started uploading test photos.
- [x] Native Graph source added with registered public client, paginated inventory, direct streaming reads, safe redirects and backend/account/client-bound new jobs. Old jobs remain rclone.
- [x] Owner reached native Graph scanning and saved a native root job; live transfer acceptance remains pending.
- [x] Add Personal Vault exclusion to the menu for new root plans and recovery of incomplete root jobs; require explicit consent, persist the choice and show the excluded scope before uploads.
- [x] Warn when a new or unstarted job overlaps existing migration scope; independent job histories cannot recognize each other's completed uploads.
- [x] Owner continued native job `54f77861b717b5e67baa2b15312eb09b` through the menu, completed inventory with Vault excluded and started parallel photo uploads. Overlapping legacy history remains independent.
- [x] Add bounded parallel file transfers (default 3, range 1-8), shared staging/disk reservations, serial media creation and shared Google retry pauses.
- [x] Sequential creation batches with durable token queues, per-item outcomes and timer/end/staging-pressure flushes.
- [x] HTTP status/operation diagnostics for retry messages, with safe fixed error categories and batch request timings.
- [x] Bounded read-only Graph HTTP retries with Retry-After, cancellation and saved-job pause deadlines on stop; permission/authentication failures remain immediate.
- [x] Menu option 10 and repeatable plan --exclude-folder; immutable per-job policy, explicit skipped records and report/preview disclosure; full scan retained.
- [x] Local reopened-journal checks for mixed batch outcomes, uncertain-only recovery, video polling without re-upload and source pause persistence. Live and abrupt-power-off gates remain unchecked.
- [ ] Benchmark native parallel-plus-batch throughput and remaining per-stage latency; verify actual multi-item recovery.
- [ ] Determine whether Personal Vault contains media needing separate migration; its inaccessible contents have unknown count/size and are outside this job.
- [ ] Check current free destination/local storage, review the completed preview and conduct a short controlled trial within that same root job.
- [ ] Live photo/video trial: compare eligible counts; inspect dates, orientation, metadata, video playback/audio and READY status.
  - [x] Owner verified that uploaded photos retain their metadata in Google Photos.
  - [x] Video `IMG_0929.mov` uploaded and created with full metadata preserved (job `3b7824a2d3a0943945ab5d9d687118ab`). Google still reports it as processing; READY, playback and audio checks remain.
  - [ ] Poll job `3b7824a2d3a0943945ab5d9d687118ab` with `resume` until the video reports READY, then verify playback and audio.
- [ ] Interrupt/resume that same job; verify confirmed photos are skipped, created videos are polled by saved media ID, and unfinished uploads recover without repeating confirmed items.
- [ ] Validate uncertain creation recovery: normal resume pauses, explicit reconciliation handles the identical staged bytes, and unresolved items remain visible in reports.
  - [x] Exercised live: a spurious HTTP 503 during `batchCreate` left the item uncertain; `resume --reconcile` verified the staged hash, re-sent identical bytes and creation succeeded (job `3b7824a2d3a0943945ab5d9d687118ab`).
  - [ ] Verify that a normal `resume` (without `--reconcile`) stops for reconciliation and that unresolved outcomes stay visible in reports.
- [ ] Run a larger non-overlapping folder or continue the same full-scope job; record throughput, disk use and report results.
- [ ] Complete the approximately 700 GB migration and reconcile all eligible/skipped/failed items.
- [ ] Native Windows/macOS credential refresh/runtime checks and disposable-VM hard-power-off acceptance.

# Detailed acceptance gates

The original acceptance gates remain below until every listed condition is validated. An unchecked gate does not mean its implementation is absent. File names are planning references; actual module layout is recorded in the plan and review. Creation batching is implemented; automatic OS boot launch is outside the current behavior. Tests cannot replace the required real-account and native-platform checks.

- [ ] 1. Bootstrap native CLI and CI build matrix.
  - Acceptance: help/version run on Windows x64 and both macOS architectures.
  - Verify: build and execute native smoke tests.
  - Dependencies: none. Files: go.mod, cmd/od2gp/main.go, internal/cli/root.go, CI workflow. Scope: medium.
- [ ] 1a. Add first-run Go detection and automatic installation.
  - Acceptance: macOS shell and Windows PowerShell launchers reuse compatible Go or install a pinned, checksum-verified official archive into a private user-owned directory automatically; no admin access or global PATH changes.
  - Verify: Go absent/present/outdated/broken, both macOS architectures and Windows x64, paths with spaces, offline download, checksum mismatch, interrupted and concurrent installation; repeated runs reuse the toolchain and preserve system Go.
  - Dependencies: 1 source scaffold (can unblock its build verification). Files: scripts/od2gp.sh, scripts/od2gp.ps1, scripts/toolchain.tsv, bootstrap tests, docs/setup.md. Scope: medium; split per platform if needed.
- [ ] 1b. Connect bootstrap to build and CLI launch.
  - Acceptance: build uses the selected Go executable, preserves arguments/exit codes and runs without shell-profile changes; packaged binaries work without Go. CI and bootstrap use a consistent toolchain policy.
  - Verify: first-run help/doctor on clean native OS environments, install/build failure never triggers login or transfer, second run works offline with cached toolchain and dependencies.
  - Dependencies: 1a. Files: launchers, CI workflow, README.md, bootstrap integration tests. Scope: medium.
- [ ] 2a. Detect/install verified rclone runtime.
  - Acceptance: reuse compatible rclone or obtain a pinned official binary with checksum verification on Windows/macOS; no system installation overwritten.
  - Verify: missing/present/broken binary, offline setup, checksum failure and native platform startup.
  - Dependencies: 1b. Files: runtime installer/manifest, setup integration and focused tests. Scope: medium.
- [ ] 2b. Configure dedicated read-only OneDrive remote.
  - Acceptance: setup uses rclone's supported default OAuth client without Azure registration, read-only consent, encrypted writable config and secure password handling; doctor no longer requires a Microsoft client ID for this source mode.
  - Verify: actual personal-account login/refresh, configuration protection, cancellation, disconnection and no credentials in process arguments/logs.
  - Dependencies: 2a. Files: source setup/credential adapter, settings, privacy/setup documentation. Scope: medium; split if needed.
- [ ] 2c. Prove rclone source enumeration and content reads.
  - Acceptance: structured inventory, stable drive/item/version binding and one-file reads into encrypted staging; bounded memory and sanitized errors.
  - Verify: Unicode paths, changed remote/account, read permission failures, pagination and interrupted/range reads.
  - Dependencies: 2b. Files: internal/onedrive/rclone.go and tests, feasibility notes. Scope: medium.
- [ ] 3. Prove Google browser sign-in.
  - Acceptance: append-only upload and app-created read access for video processing checks, refresh, secure tokens and account binding.
  - Verify: real login/refresh and wrong-account rejection.
  - Dependencies: 2c. Files: internal/identity/google.go and tests, docs/setup.md. Scope: small.
- [ ] 4. Prove photo/video transfer and encrypted staging.
  - Acceptance: Representative photos and videos arrive, including a multi-gigabyte video reaching READY; dates, rotation, playback/audio inspected; same-bytes replay behavior recorded; staging bounded and encrypted.
  - Verify: real-account smoke tests and restart of partial staging on both platforms.
  - Dependencies: 3. Files: provider transfer adapters, staging implementation/test, feasibility notes. Scope: medium; split adapters if needed.
- [ ] Checkpoint A: authentication, metadata and recovery feasibility demonstrated before scaling.
- [ ] 5. Add journal and job locking.
  - Acceptance: persistent journal outside disposable caches, WAL/FULL durability, protected session data, account-bound resume and OS-held exclusive job locks.
  - Verify: reopen-after-crash, applied durability settings, journal corruption refusal and concurrent-open tests.
  - Dependencies: 2c, 3; required before crash-recovery claims in transfer work. Files: internal/journal/schema.sql, store.go, store_test.go, lock.go. Scope: medium.
- [ ] 6. Add recursive inventory.
  - Acceptance: pagination, nested folders, photo/video classification, size-limit checks, unsupported-file reasons and source versions captured.
  - Verify: mocked multi-page tree including Unicode, duplicate names and inaccessible folders.
  - Dependencies: 5. Files: internal/onedrive/scan.go, scan_test.go, internal/migration/inventory.go. Scope: medium.
- [ ] 7. Expose read-only migration preview.
  - Acceptance: manifest contains correct totals, account identities, estimates, photo/video totals, largest-file cache requirement and skipped files; compares source bytes with supplied available destination storage (or marks capacity unverified); zero destination writes.
  - Verify: CLI fixture tests and representative real-folder preview.
  - Dependencies: 6. Files: internal/cli/plan.go, plan_test.go, internal/migration/manifest.go. Scope: medium.
- [ ] Checkpoint B: inventory reconciles with source fixture and preview is read-only.
- [ ] 8. Add bounded transfer execution.
  - Acceptance: configurable 30 GiB default cache cap for the approximately 700 GB source, bounded video memory use, source-version checks, hashing and backpressure work; files larger than the cache budget produce actionable errors.
  - Verify: slow-network/disk-full/source-edit tests and staging-cap assertions.
  - Dependencies: 7. Files: internal/migration/runner.go, runner_test.go, internal/onedrive/download.go and tests. Scope: medium.
- [ ] 9. Add resumable upload and creation batching.
  - Acceptance: recover video chunk offsets/tokens and expired sessions, respect chunk granularity, serialize creation, persist every per-item result.
  - Verify: fake-server partial batches, expired tokens and upload interruptions.
  - Dependencies: 8. Files: internal/googlephotos/upload.go, create.go and tests. Scope: medium.
- [ ] 9a. Track video processing to readiness.
  - Acceptance: saved media IDs are polled with backoff; READY completes the video; pending and failed processing are reported separately without automatic re-upload.
  - Verify: mocked delayed success, processing failure, missing status and restart while pending, plus real video playback.
  - Dependencies: 9. Files: internal/googlephotos/status.go, status_test.go, internal/migration/processing.go and tests. Scope: medium.
- [ ] 10. Add restart and uncertain-outcome recovery.
  - Acceptance: reopening offers the unfinished job as the default resume choice; explicit resume continues committed progress after shutdown/power loss; completed items are skipped and lost-response outcomes reconciled or explicitly reported. Persisted files/keys must survive reboot; no auto-launch at boot is promised.
  - Verify: abrupt process kills before/after every network mutation, fsync, journal commit and cleanup; torn encrypted chunks, expired sessions, pending videos and surviving source workers; real same-bytes replay and a disposable VM hard-power-off/restart exercise.
  - Dependencies: 9a. Files: internal/migration/recovery.go, recovery_test.go, internal/cli/resume.go. Scope: medium.
- [ ] 11. Add provider-specific retries and clean cancellation.
  - Acceptance: bounded retries, Retry-After, quota pauses, token refresh and Ctrl+C checkpoints.
  - Verify: rate-limit/auth/network fault tests and real cancellation.
  - Dependencies: 10. Files: internal/migration/retry.go, retry_test.go, runner.go, internal/cli/run.go. Scope: medium.
- [ ] Checkpoint C: no silent omissions or false completion under injected failures.
- [ ] 12. Add progress and reports.
  - Acceptance: status and JSON/CSV reports reconcile every item; secrets redacted; stable exit codes; processing videos keep the job incomplete.
  - Verify: output fixtures and 10,000-item synthetic manifest, including CSV formula-safe handling.
  - Dependencies: 11. Files: internal/cli/status.go, report.go, report_test.go, docs/usage.md. Scope: medium.
- [ ] 13. Package and document personal-use beta.
  - Acceptance: local native artifacts/checksums, personally owned OAuth client setup and token-lifetime guidance, recovery and local-data cleanup docs; fresh-machine walkthrough succeeds.
  - Verify: install/run on actual Windows and macOS; tests, race checks where supported, vet and build pass.
  - Dependencies: 12. Files: release workflow, README.md, docs/setup.md, docs/recovery.md. Scope: medium.
- [ ] Checkpoint D: mixed photo/video migration reaches completed/READY on both OS families; power-loss restart/report guarantees and cache bounds are demonstrated.

Confirmed scope: personal use with photos and videos in version one. Public distribution and signing/notarization are outside scope. Optional features: album mapping and incremental scanning, each with its own acceptance criteria.

Owner live evidence (2026-10-18): native job `54f77861b717b5e67baa2b15312eb09b` completed inventory (1,866 files, 630 skipped, 40,567,884 eligible bytes) and started uploading photos with Vault excluded. Its saved cap is 30 GiB. Parallel transfer changes still require a live restart/benchmark.
