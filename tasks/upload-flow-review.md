# Upload flow performance review

Reviewed 2026-11-01 against 29c3a55. This is a source review and local regression check, not a measurement of the owner's running Pictures job. No provider calls, account changes or running-process changes were made.

## Current flow

Each of 1–8 file workers checks source version, downloads the complete file into encrypted staging, checks source version again, starts/resumes a Google session, uploads chunks (target 16 MiB), and saves the resulting token. The coordinator collects tokens and creates media in batches of at most 50, flushing on the queue timer (default 2 seconds), completion or staging pressure. Successful IDs are saved before staging cleanup. Videos are polled after creation and subsequently through the wait loop.

## Findings and ordered work

### 1. Keep scheduling while a batch is being created

Evidence: `internal/migration/parallel.go` calls `commit()` synchronously from its scheduling loop; that invokes `commitBatch`. In-flight workers can finish, but the coordinator cannot consume their results or dispatch replacement files until commit returns. This is a structural scheduling gap; its share of real elapsed time is unknown.

Implement a single dedicated committer and bounded completion channel. Keep scheduling uploads while a batch is outstanding, maintaining exactly one batchCreate per account. The coordinator alone must continue owning staging reservations. Retain reservations for queued/in-flight batches until a successful journal checkpoint and actual staging deletion have been acknowledged. Bound queue depth, memory, staging and token age. Do not introduce concurrent batchCreate calls.

Acceptance: fake a blocked batch request while there is spare staging; prove further files upload, peak create concurrency stays one, cancellation drains results, ambiguous creation remains uncertain, and reopen never recreates confirmed IDs. Compare throughput against current code with identical injected service delays and file set.

### 2. Remove video polling from the commit critical path

Evidence: `internal/migration/batch.go` calls `transferWithRetries` sequentially for every created item. `engine.go:created` immediately calls `processing` for videos, issuing a GET for each. A slow status check/retry can delay dispatch and subsequent batch creation even though upload and media creation succeeded. This concerns the initial GET/retries, not a synchronous wait until every video becomes READY.

Persist media IDs and processing state, clean up staged content, then poll videos in a separate bounded status phase/queue. Preserve READY/FAILED reporting and resume by saved media ID. A status-check failure must remain visible and resumable; it must not imply upload failure or trigger re-upload.

Acceptance: delayed status calls do not stop subsequent upload/creation; saved video IDs survive interruption; READY/FAILED/error outcomes remain accurate.

### 3. Measure batch fill before changing defaults

A 2-second flush can produce batches far smaller than 50 on a slow source. That is legitimate for responsiveness, but increases request count. Record mean/p95 batch size, flush reason (full/timer/staging/end), commit duration and queue age. Then compare 2/5/10-second queue waits on the same fixture. Staging pressure, tail completion and token expiry still require early flush. No blanket claim that every request must contain 50 items.

### 4. Optional raw upload for small photos

Currently even a small image requires Google session-start plus upload/finalize requests. Google's simple raw upload can return a token in one request. This may benefit a library of many tiny images; it cannot be assumed to dominate a collection of large videos.

Benchmark a small-file threshold using original staged bytes. Keep resumable uploads for large files and fallback/recovery; persist tokens before queuing creation. Retry of raw bytes must never automatically retry an uncertain media creation. Use a bounded body buffer and retain encrypted staging until confirmed creation.

### 5. Separate download and upload stages only if measured

A file worker downloads its entire file before uploading it, occupying one worker slot across both phases. Across files the phases already overlap. Dedicated bounded download and upload queues could improve utilization when all workers are downloading simultaneously, but could also saturate disk/network or fill staging.

Measure per-stage busy/idle time first; implement separate concurrency limits only if that shows a material gap. Prefer bounded encrypted staging over introducing uncheckpointed direct streaming.

## Measurement first

Add a per-run summary: downloaded/uploaded bytes and rates, files/minute, time in source metadata/download/staging/upload/session-start/batch-create/status polling, queue depth, batch size/flush reason, staging high-water mark, HTTP 429/5xx counts and provider-wait time. Distinguish transfer throughput from Google video-processing time. Never log auth headers, upload tokens, session URLs or continuation URLs.

For synthetic tests compare identical fixtures and injected latency, repeated runs, total elapsed time, request count and peak allocations. For live validation use a small representative next job after the current job finishes, or continue the same job following an intentional pause. Independent jobs do not share completion history and can cause duplicates.

Keep source version checks, durable encrypted checkpoints, Retry-After pauses and uncertain-creation recovery. Raising worker count, deleting fsyncs or removing version checks is not an evidence-backed optimization.

## Official references

- https://developers.google.com/photos/library/guides/upload-media — parallel byte uploads, serial media creation per user, batches up to 50, token lifetime.
- https://developers.google.com/photos/library/guides/resumable-uploads — small-file simple upload option, resumable offsets/chunk alignment and session lifetime.

## Validation

Local targeted race checks cover batch sizing/tail, timer/staging flushes, parallel worker bounds, interruption/resume, partial results, uncertain outcomes and shared provider pauses. These validate existing safety behavior; they do not prove real-account throughput or the proposed scheduling changes.

## Implementation update

The first two changes are implemented: one asynchronous committer, bounded queued tokens and staging reservations, and video polling after the transfer pass. Cache is removed only after media IDs are durably saved; ambiguous outcomes retain staged bytes. A blocked-batch test failed on the previous scheduler (no second upload within 200 ms while commit was blocked) and passes with the new scheduler. A one-file-cache test verifies six transfers finish, then video polling sees no remaining staging files and reaches READY. Existing partial/uncertain/cancellation and reopen tests remain required.

Initial diagnostics now report per-file download/version-check and session/upload durations, batch queue age/flush reason, logical batch/item count and staging reservation peak/release. Full HTTP counters, per-stage busy/idle utilization, physical disk bytes and real-account throughput remain unmeasured. Small-file raw uploads, changed batch defaults and separate download queues remain measurement-gated experiments; none are enabled by this change.
