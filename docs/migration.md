# Transfer photos and videos

The first transfer engine is implemented. Automated tests use simulated provider responses; no real media transfer has been verified yet. Begin with a small folder containing a photo and a video before selecting the full library.

Follow the [step-by-step operating instructions](runbook.md) for account checks, scope selection, a live trial, full migration and recovery. This document describes the transfer behavior and limits in more detail.

## Preview

From the project directory on macOS:

```sh
bash scripts/od2gp.sh plan --source /Pictures/Test --cache-gib 30
```

On Windows replace `bash scripts/od2gp.sh` with `.\scripts\od2gp.ps1`.

This reads OneDrive metadata, verifies source/destination identities and saves an encrypted job journal. It uploads no media. `/` selects all OneDrive folders; `/Pictures/Test` selects that folder recursively. A plan counts eligible media and records unsupported/oversized files as skipped. It prints a generated `JOB_ID`; substitute that actual value in subsequent commands.

Google uploads use original quality and count toward your account storage. The reported 5 TB subscription allowance is not a measurement of remaining free space. The CLI does not query account-wide storage.

## Run

```sh
bash scripts/od2gp.sh run --job JOB_ID
```

The CLI displays a summary and asks you to type `yes` before upload. It downloads and uploads up to three files concurrently through encrypted staging by default, checks the source version, uploads bounded chunks to Google Photos and records the created destination ID. Original OneDrive files remain untouched. `--yes` permits explicit unattended startup.

Add `--wait` to keep the command open while Google processes uploaded videos, for up to 30 minutes after uploads finish:

```sh
bash scripts/od2gp.sh run --job JOB_ID --wait
```

The cache defaults to 30 GiB for the selected job. It needs enough disk for the largest eligible file plus encryption overhead and a journal reserve; it does not download all 700 GB at once. The manifest lives under the user config directory at `od2gp/jobs/JOB_ID/progress.db`, separate from temporary build output. Job keys remain in the OS credential store. Keep both the journal and keys to resume. Do not move the journal between computers without a designed key migration process.

## Interruptions and restart

```sh
bash scripts/od2gp.sh status
bash scripts/od2gp.sh resume --job JOB_ID
bash scripts/od2gp.sh status --job JOB_ID
```

Resume continues the saved job without asking the initial upload confirmation again. Confirmed completed items are skipped. An interrupted download restarts that one file; an active upload queries Google's accepted byte offset before continuing. A finalized session whose upload-token response was lost can require re-uploading that unfinished file. An expired session also needs a new upload. Pending videos are checked by saved destination ID, without re-uploading them. Ctrl+C preserves already committed progress.

SQLite uses WAL and FULL synchronization; inventory metadata is committed in batches of 100, while transfer state is committed at every chunk/state checkpoint; staging data is flushed before its complete state is committed. Missing/incomplete staging is downloaded again where safe. Actual filesystem/device flush behavior and real power-loss survival still require a hard-power-off VM test; disk damage or deleted keys cannot be recovered by the CLI. Automatic launch at boot and automatically choosing a saved job on startup are not implemented: reopen the CLI and use the resume command.

If a network interruption occurs during destination creation, Google may have created the item without the CLI receiving its ID. Status identifies these as **uncertain destination creations**. Inspect the report, then explicitly allow an identical-byte retry:

```sh
bash scripts/od2gp.sh resume --job JOB_ID --reconcile
```

The CLI verifies the encrypted staged content's hash and retries the same bytes. Google documents that identical media bytes resolve to the same media ID, but real reconciliation behavior still needs verification. Normal resume stops at this state rather than silently claiming success or retrying uncertain creation.

Temporary Google Photos failures now receive up to five automatic retries per file. This covers HTTP 429, HTTP 5xx and recognized network interruptions during recoverable operations. Delays increase from 30 seconds to 1, 2, 4 and 5 minutes, with a small random addition. A longer `Retry-After` always takes precedence. The pause is saved before waiting and survives cancellation or restart; resume waits for a saved pause before transferring. A pause longer than 15 minutes stops the command and prints when to resume. Retry limits reset for each file and each new invocation.

Use `--retries 0` to disable automatic retries, or choose any count from 0 to 10:

```sh
bash scripts/od2gp.sh resume --job JOB_ID --wait --retries 5
```

Each retry loads the last committed file state. Upload recovery queries the server's accepted offset; a known HTTP 429 rejection during media creation retains its upload token for retry. Unknown creation outcomes still stop for explicit reconciliation, even when `--reconcile` was supplied: the option does not authorize a loop of uncertain creations. Authentication/permission failures and unclassified errors stop rather than retrying automatically. The policy follows Google's [retry guidance](https://developers.google.com/photos/overview/best-practices) and [resumable upload protocol](https://developers.google.com/photos/library/guides/resumable-uploads).

Exhausted retries leave saved progress and the next allowed retry time. Persistent quota exhaustion or Google storage exhaustion needs external resolution. Expired/revoked credentials require `auth login google` again using the same account and OAuth client; job progress is retained. Automatic retries added here apply to the Google Photos transfer operations; source and initial sign-in/preflight errors stop the command.

Changed source files stop the job rather than replacing the planned content. An incomplete initial inventory restarts its scan before any upload. A new plan is needed for intentional source changes. Jobs are independent: creating a new plan does not reuse the old job's completion history; use resume for interruptions.

## Reports and video status

```sh
bash scripts/od2gp.sh report --job JOB_ID --format csv > migration-report.csv
bash scripts/od2gp.sh report --job JOB_ID --format json > migration-report.jsonl
```

JSON output is one object per line. Reports include source path, size, state, destination ID and reason; session URLs and upload tokens are omitted. CSV formula-like values are escaped. Reports contain personal filenames: keep them private.

Google may need time to process videos after creation. By default, the engine checks each pending video once per run; use resume later until they report READY. With `run --wait` or `resume --wait`, it checks again after 30 seconds, then 1, 2, 4 and at most 5 minutes between subsequent checks. Each command waits for at most 30 minutes after the initial transfer pass; unfinished videos remain saved for another resume. Pending videos are never uploaded again by these checks. Ctrl+C stops waiting and preserves saved progress; restart with `resume --job JOB_ID --wait`. Keep the computer awake and connected while the command runs. Temporary status-check failures use the configured retry limit; other errors or exhausted retries stop with saved progress. `--wait` controls video-readiness polling, while `--retries` controls temporary-failure recovery.

A processing failure is reported separately. Exit codes: 0 finished (possibly with skipped files), 1 failure/interruption, 2 invalid command, 3 videos still processing (without `--wait` or after its time limit), 4 video processing failures. Review the report before considering the migration complete.

## Current limits

Transfers use three workers by default (`--workers 1-8`); media creation requests are sequential and contain one item. Efficient creation batching and complete destination-library deduplication are not implemented. The menu offers saved jobs for continuation. Supported files are selected by known extensions; Google makes the final format/codec validation. Source version checks compare stable item ID, size, modification time and backend metadata. Native Graph includes eTag/cTag values; legacy rclone uses its exposed hashes. A saved job retains its backend, so these different version formats are never silently mixed. New Graph jobs also bind to the Microsoft account and OAuth client.

## Parallel transfers

`run` and `resume`, including menu transfers, default to three parallel files. Choose `--workers 1` for serial operation or a count up to eight. This changes the current run only; the same saved job, scope and account bindings are retained.

```bash
bash scripts/od2gp.sh resume --job JOB_ID --wait --workers 3
```

Stop an existing run with Ctrl+C and wait for it to exit before starting the updated launcher. Do not create a new plan to change worker count. Completed items remain skipped. In-progress files retain their individual resumable checkpoints; a partial source download restarts that file.

All workers share the job's staging cap, including files retained after interruption. The scheduler reserves encrypted sizes before starting work and reduces concurrency when files cannot fit together. It also reserves available disk space for pending writes and leaves 128 MiB for the journal. Google library creation stays sequential, as recommended in [Google's upload guidance](https://developers.google.com/photos/library/guides/upload-media); upload bytes may overlap. Google retry pauses are shared and persisted. On an unrecoverable error or cancellation, dispatch stops and in-flight workers finish stopping before the command exits. Uncertain destination creation still requires explicit reconciliation.

Parallelism does not speed up the initial inventory or guarantee a particular throughput. Network bandwidth, provider throttling, encryption and sequential media creation can limit gains. Start with three workers and measure a representative photo/video trial before increasing it. Creation batching remains a follow-up.
