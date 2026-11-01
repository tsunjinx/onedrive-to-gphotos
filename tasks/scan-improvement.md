# Faster full-drive inventory

Status: delta inventory and atomic page checkpoints implemented for saved root Graph jobs; local recovery tests pass. Native Windows CI and live throughput validation are tracked separately. User reports approximately 700 files in 15 minutes on the production Windows source account. This measures observed file-count throughput, not API request latency or a confirmed throttling rate.

## Findings in current code

- `internal/msgraph/client.go:List` traverses folder children using `$top=200`. Prefetch overlaps first pages of up to three pending folders, but later pages remain sequential. Completed sibling pages can occupy the prefetch slots while DFS follows a deep branch. A synchronous pagination request can also overlap outstanding prefetch requests, so three buffered folders is not a strict global three-request bound.
- `internal/migration/engine.go:Discover` resets incomplete inventory on restart. It commits records in batches of 100, but records alone are insufficient to resume traversal.
- `internal/migration/filter.go` deliberately lists excluded-folder contents and records individual skips. Current exclusions do not prune traversal (apart from the special root Personal Vault handling).
- File counts alone cannot distinguish a slow response, many empty/nested folders, disk commit time, or Retry-After waits. There is no evidence yet identifying which dominates the user's account.

## Recommended implementation order

### 1. Measure the bottleneck

Add a safe scan observer: request count, response duration, status, retry count, accumulated wait, pages, folder/file counts and database commit duration. Emit a heartbeat every 10 seconds, including when file count does not change. Never log bearer tokens, signed URLs, raw response bodies or delta/continuation URLs. Keep output serialized. Compare the current traversal against delta using the same fixed test hierarchy; do not run simultaneous scans on the production account.

### 2. Delta for full-drive scans

Use `GET /drives/{drive-id}/root/delta` for a new full-drive inventory, following server continuation links until a final delta link. Keep the existing children path for interactive folder browsing and initially for narrow folder migrations. Do not use `token=latest` for initial enumeration.

Microsoft documents delegated `Files.Read` for personal accounts and delta enumeration of the drive hierarchy. Its scan guidance recommends delta for initial crawling. These are paginated calls, not a promise of a one-request scan. Sources:
- https://learn.microsoft.com/en-us/graph/api/driveitem-delta?view=graph-rest-1.0
- https://learn.microsoft.com/en-us/onedrive/developer/rest-api/concepts/scan-guidance?view=odsp-graph-online

Expected benefit is fewer per-folder calls, especially with many small folders. Illustrative request model only: 1,000 folders each holding 10 files costs roughly 1,001 children requests; 11,000 items at an assumed 200 items/page would cost about 55 delta pages. Actual page sizes, metadata hydration calls and service latency must be measured; this is not a throughput guarantee.

Resolve hierarchy from stable IDs and parent references after collecting pages. Handle repeated IDs using the last returned state, deletion facets, parent-after-child order and folder moves. Validate paths, drive binding, source versions, cycles and missing parents. Hydrate missing required metadata explicitly or fail incomplete. Continue to enforce Vault policy and reject unsupported remote shortcuts; delta must not silently broaden or shrink coverage.

### 3. Durable page checkpoints

Persist encrypted raw inventory entries and the next-page cursor atomically in SQLite, bound to account, drive, scope, exclusion policy and scanner version. Replayed pages must be idempotent. Store cursors encrypted and exclude them from reports. Finalize resolved items and inventory-complete status only after a validated final page. No uploads from partial inventory.

An invalid/expired cursor (including HTTP 410) must trigger an explicit inventory resync; never report partial data as complete. Existing completed upload records must survive. Old incomplete jobs have no usable page cursor and require one restart; never pretend they can resume at file 1100.

### 4. Optional subtree pruning

Introduce an explicit exclusion mode that avoids traversing excluded subtrees in children scans. Persist that policy and report their count/size as unknown, instead of silently replacing the current per-file skipped-report semantics. For full-drive delta, exclude during local resolution; do not claim this prevents the server returning excluded metadata.

## Acceptance gates

- Deterministic fixtures: wide/deep trees, multi-page responses, duplicate IDs, deletions, moves, missing parents/versions, root Vault versus nested same-name folders, hostile/cyclic continuation links and empty intermediate pages.
- Kill after page data but before cursor commit, and after atomic commit; restart without missing/duplicating entries. Validate expired-cursor resync and account/scope mismatch rejection.
- Preserve provider Retry-After pauses, cancellation and bounded memory. Keep raw inventory on disk for large libraries.
- Same final eligible/skipped scope on a stable fixture; record request count, wall time and peak memory for children versus delta.
- Run native Windows tests. Then measure a representative live read-only trial before selecting delta by default for the main account. No credential or migration changes are part of this research.

## Implementation evidence

- Root saved Graph jobs use delta; narrow folders, browsing and standalone diagnostics retain children enumeration.
- Raw pages are encrypted by stable-ID hash in SQLite; page entries and continuation commit atomically. Incomplete item records can be rebuilt from saved pages without network replay.
- Tests cover reopen/resume, transaction rollback, duplicate latest state, late parents, missing metadata, hierarchy cycles, root versus nested Vault, empty intermediate pages, cancellation, HTTP 410 and hostile continuations. Engine integration verifies the incomplete-upload gate and resizing staging without invalidating the scan.
- A stable fixture with 40 folders and one photo each returns identical files: 41 children requests versus one delta request. This is request-count evidence, not a live speed multiplier.
- Diagnostics currently include page entry count/duration, HTTP retry status/delay and a 10-second Graph-wait heartbeat. Fine-grained DB timing, peak-memory benchmarks, explicit subtree pruning, a real process-kill checkpoint test and representative live throughput remain follow-up gates; they are not claimed complete.
