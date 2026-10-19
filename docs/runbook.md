# Operating instructions: OneDrive → Google Photos

Use this guide for the current CLI. It copies original photos and videos and keeps the OneDrive files. Both account connections already work according to the owner. Real uploads still need a small trial before the approximately 700 GB migration.

## Native Graph setup and existing jobs

New jobs now use native Graph. Run `bash scripts/od2gp.sh setup microsoft dd4ceec5-81a2-4c43-b4f7-5680436926aa`, then `auth login onedrive` and `source check`. See [native setup](setup.md) for the desktop redirect and permissions. These steps preserve the Google configuration. Source commands use Graph by default; use `OD2GP_SOURCE_BACKEND=rclone` for legacy source diagnostics or selection. Continuing a saved migration automatically uses that job's backend.

The existing test job remains rclone. Use a non-overlapping ordinary folder for the first new Graph test; there is no cross-job duplicate detection. Native Graph removes per-file subprocess launches, but file transfers now use three workers by default and Google creation calls stay sequential; benchmark before assuming a 700 GB completion time.

## Recommended: use the menu

Open **od2gp.command** on macOS or **od2gp.cmd** on Windows with a double-click. You can also use the terminal commands below.

Open your project directory and run:

```sh
bash scripts/od2gp.sh menu
```

On Windows PowerShell:

```powershell
.\scripts\od2gp.ps1 menu
```

1. Choose **1** to check both connections. If needed, **6** opens account setup/sign-in options.
2. Choose **2** for a new migration. First pick the OneDrive drive by number; the current drive is marked, and **0** cancels without changing it. Review its folders and sample photo/video filenames: **1** uses this drive, **2** chooses another, **3** inspects a folder and **0** cancels. Then enter its folder and accept the default 30 GiB staging cap, or enter a different cap. The CLI builds the preview, then displays the existing upload confirmation. Type `yes` to start; any other response leaves the plan saved without starting uploads.
3. Choose **3** to continue a saved job by number. Jobs show the folder, counts, creation time and a short ID; unfinished jobs appear first. A plan that has not started asks for confirmation; an interrupted transfer resumes its saved progress. If planning failed during inventory, continue that same job: the scan restarts from the beginning, completes the file counts, then asks for upload approval. The saved folder and staging cap are retained.
4. Choose **4** for progress or **5** to save a CSV/JSONL report. Report export prompts for a filename and refuses to overwrite an existing file.
5. Press Enter after an action to return to the menu; **0** exits. When an unfinished readable job exists, Continue is the suggested main-menu choice, and the first unfinished job is suggested in the job list. No transfer starts merely by opening the menu.

Use **7 — Choose OneDrive drive** to reopen drive selection without creating a new job. The same action is under **6 → 5**. It lists drives available to the connected Microsoft account, not local Windows disk letters. Your main personal library is usually named **OneDrive (personal)**; archive/metadata entries may also appear. Verify the choice by recognizing actual folder and photo/video filenames in the preview, rather than relying on the drive name. Selection reuses your stored sign-in and keeps read-only access. A checked encrypted copy replaces the active source settings only after success; cancellation or a failed check keeps the previous settings. Stop an active transfer before changing drives.

Each inspection shows up to 20 folders from the first 200 entries and up to 10 media examples from at most 2,000 checked filenames, with a shared 15-second time limit. It reads metadata only and downloads/uploads no media. An empty sample does not prove the drive contains no photos; inspect a known folder such as `/Pictures/Camera Roll`, or choose another drive. Inspection chooses the drive only; enter the migration folder separately when creating the plan.

Existing jobs stay bound to their original drive. To resume a job from another drive, select its original drive first; choose a separate new plan to migrate a different drive. Drive selection does not switch Microsoft accounts; use account setup/sign-in for account issues.

By default, menu transfers use three parallel files, sequential creation batches of up to 50 items, video polling and five retries per operation; option 8 remembers your overrides. Use the commands below for custom retry options or explicit uncertain-creation reconciliation. Keep the same scope/storage/media checks described below. Running the launcher without arguments opens the menu in a terminal too; explicit `menu` also works in environments where terminal detection is unavailable.

The numbered sections below document the equivalent commands and validation steps.

## 1. Open the project and check the accounts

On your Mac:

```sh
cd /path/to/onedrive-to-gphotos
bash scripts/od2gp.sh auth check onedrive
bash scripts/od2gp.sh auth check google
```

On Windows, open PowerShell in your checkout:

```powershell
Set-Location 'C:\path\to\onedrive-to-gphotos'
.\scripts\od2gp.ps1 auth check onedrive
.\scripts\od2gp.ps1 auth check google
```

In the remaining examples, Windows users replace `bash scripts/od2gp.sh` with `.\scripts\od2gp.ps1`. Replace the example Windows project path with your actual checkout. The source launcher detects Go and installs its pinned toolchain if needed; the first installation/build requires network access.

Both checks must succeed. `doctor` alone only checks local configuration. If the checks work, skip setup and login. If necessary, use [account setup](setup.md), then rerun the checks:

```sh
bash scripts/od2gp.sh setup
bash scripts/od2gp.sh auth login onedrive
bash scripts/od2gp.sh auth login google
```

During OneDrive setup, use the local browser and choose the entry named **OneDrive (personal)**. Check that the Google browser login uses your intended destination account. Keep the same accounts and Google OAuth client for later resume.

For manual command use, choose or change the source drive before planning:

```sh
bash scripts/od2gp.sh source select-drive
```

This displays the drive picker even when the existing connection already works. It transfers no media. The plain `plan` command uses the currently selected drive; the interactive menu asks you to choose one for each new migration.

If your Google OAuth app remains in external Testing status, its refresh token normally expires after seven days for the requested Photos scopes. Reauthorize the same account/client when needed, then resume the saved job. Your storage subscription does not extend this token lifetime. See [Google's token-expiration rules](https://developers.google.com/identity/protocols/oauth2#expiration).

## 2. Decide the job scope and check storage

Choose a source folder as it appears inside OneDrive. `/Pictures/Test` below is an example: replace it with an existing folder. Quote paths containing spaces. `/` selects the entire drive recursively; non-media and files outside the CLI's supported types/size limits are reported as skipped.

For the first live trial, use a small existing folder with a few photos and at least one video. Inspect a representative HEIC/rotated photo and a larger video if your library contains them.

A job's folder is fixed. Continue it with its original job ID. Separate plans do not share completion history. For the remaining library, choose non-overlapping folder jobs. A new `/` plan after a separate folder trial includes those trial files again; whole-library duplicate detection is not implemented. If you want one job for the entire drive, plan `/` once and use a brief initial run followed by Ctrl+C as its trial, then resume that same job. A representative separate-folder trial remains the clearer way to validate both media types.

Check the Google account's **remaining free storage**, rather than just its subscription allowance. The reported 5 TB allowance is not a measurement of free capacity. Compare available space with the preview's eligible bytes; leave room for other account use. The CLI does not query account-wide free storage.

The default staging cap is 30 GiB. All workers share that cap, so 700 GB of free local disk is not needed. Each file must fit its cap plus encryption overhead, and disk must have a further 128 MiB journal reserve. For the bulk run, having at least 30 GiB free is a practical starting point; keep space for other applications too. Preview prints the largest eligible staged file and warns if its cap is too small. The cap is fixed when planning; change it in a new plan before uploading, rather than passing a different cap to resume.

Keep the computer powered, awake and online, and keep the Terminal/PowerShell session open during a run. The CLI does not prevent sleep or start itself after reboot.

## 3. Preview without uploading

```sh
bash scripts/od2gp.sh plan --source "/Pictures/Test" --cache-gib 30
```

This verifies the account bindings, scans metadata and saves an encrypted job. It uploads no media. Review the file count, eligible bytes, skipped count and staging requirement. Save the generated **32-character job ID**.

Replace `JOB_ID` in every following command with that actual ID. Do not run another plan to continue this job. An interrupted inventory can also be resumed using the saved ID.

To see why individual files were skipped before uploading:

```sh
bash scripts/od2gp.sh report --job JOB_ID --format csv > pilot-report.csv
```

## 4. Run the trial

```sh
bash scripts/od2gp.sh run --job JOB_ID --wait --retries 5
```

Read the summary and destination account, then type `yes`. Downloads and uploads appear as progress. Use `run` for the initial confirmation and `resume` for subsequent continuation.

- `--retries 5` permits up to five retries per file for recoverable Google failures. Delays increase from about 30 seconds to five minutes; a longer provider pause takes precedence. Pauses above 15 minutes stop with a time for a later resume. `--retries 0` disables automatic retries.
- `--wait` polls pending videos for up to 30 minutes after the initial transfer pass. It does not limit the duration of the uploads themselves. If videos are still processing afterward, resume later; they are checked by destination ID without being uploaded again.
- Source, sign-in, permanent and uncertain-creation errors stop with saved progress. Do not repeatedly create new plans to bypass a failure.

For a root job used as its own trial, press Ctrl+C after a few items have completed and inspect those items before continuing. Files may be encountered in an order that does not include a video early; do not treat a photo-only sample as proof of video compatibility.

## 5. Inspect the result before scaling

```sh
bash scripts/od2gp.sh status --job JOB_ID
bash scripts/od2gp.sh report --job JOB_ID --format csv > pilot-report.csv
```

Open Google Photos in the destination account and check:

1. The expected photos appear, with acceptable dates, orientation and image appearance. Images may appear at their capture dates rather than at the end of the timeline.
2. Videos play, have audio and correct rotation, and reach `completed` in the report after Google reports READY. `processing` remains unfinished.
3. Eligible counts reconcile with completed/pending/failed records; every skipped item has an understandable reason. OneDrive originals remain available.

The CLI transfers original bytes into Google's upload API. Successful creation does not establish a byte-for-byte downloadable archive or guarantee that missing capture-date metadata will be repaired.

Then exercise restart on the same job: press Ctrl+C during an unfinished transfer, reopen the terminal if needed, and run the resume command below. Completed items should stay completed. Do not cut power deliberately on your working computer; actual power-loss acceptance is a separate disposable-VM test.

## 6. Resume after interruption or shutdown

From the same checkout and OS user account:

```sh
bash scripts/od2gp.sh status
bash scripts/od2gp.sh resume --job JOB_ID --wait --retries 5
```

`status` lists saved IDs if you forgot one. Resume preserves committed progress, skips completed items, restarts the interrupted download if needed, and queries the server offset for an active upload. It may need to re-upload an unfinished file after a finalized/expired upload session. The computer must be restarted and the command run manually after shutdown.

Keep the journal, credential-store keys and original Google client JSON. Default persistent app directories are `~/Library/Application Support/od2gp/` on macOS and `%APPDATA%\od2gp\` on Windows; each job is under `jobs/JOB_ID/`. Keys stay in the OS credential store, so copying only that directory to another machine does not migrate a job. If you use `OD2GP_CONFIG_DIR`, keep the same override for subsequent commands. Do not remove job data or keys while it is unfinished.

| Message or state | Action |
|---|---|
| Temporary Google failure | Let the bounded retries run; if they stop, resume after the printed pause |
| Google sign-in expired/revoked | Run `auth login google` for the same account/client, then resume |
| OneDrive access failure | Check connectivity, run `auth check onedrive`, reconnect the same drive if needed, then resume |
| Source changed since planning | Stop and review the changed item; a new plan has independent history and may include previously imported files |
| File exceeds cache capacity | Before transfer, make a new plan with a sufficient cap; resume cannot resize an existing job |
| Videos still processing | Resume later with `--wait`; keep the original destination IDs |
| `processing_failed` | Review the video/codec and report; no automatic re-upload occurs |
| Uncertain destination creation | Inspect status, report and Google Photos; use explicit reconciliation only after review |

For an uncertain creation, retain the original encrypted staging and job keys. The explicit recovery command is:

```sh
bash scripts/od2gp.sh resume --job JOB_ID --wait --retries 5 --reconcile
```

It verifies the staged hash and permits an identical-byte retry. It does not authorize an automatic loop of uncertain creations. Real same-bytes reconciliation remains a live validation gate; normal resume stops at this state.

## 7. Continue to the full library and finish

After the trial, media inspection and restart check succeed, continue the same full-scope job or plan the next non-overlapping folder.

The bulk migration may span multiple days. Google documents a default Library API quota of 10,000 requests per project per day, including uploads; the storage subscription does not replace that request quota. Actual duration depends on eligible file count, file sizes, network throughput and pauses. See [Google's quota documentation](https://developers.google.com/photos/overview/api-limits-quotas).

For a new whole-drive job, the preview is:

```sh
bash scripts/od2gp.sh plan --source "/" --cache-gib 30
```

Use the **new ID printed by this plan** in `run`; retain any earlier IDs. A whole-drive job includes earlier trial folders. Do not start an overlapping new job as a shortcut for resume.

For an already planned full-library job:

```sh
bash scripts/od2gp.sh resume --job JOB_ID --wait --retries 5
bash scripts/od2gp.sh status --job JOB_ID
bash scripts/od2gp.sh report --job JOB_ID --format csv > migration-report.csv
bash scripts/od2gp.sh report --job JOB_ID --format json > migration-report.jsonl
```

You can run status/report from a second terminal in the project directory during a transfer. Keep one active run per job. Reports include personal filenames; store them privately.

Finish only after eligible items are completed, no videos remain pending, and every skipped/failed item is reviewed. An exit code of 0 can still include skipped files.

| Exit code | Meaning |
|---|---|
| 0 | Finished; review skips |
| 1 | Failure or interruption; review the message and saved job |
| 2 | Invalid arguments/setup command usage |
| 3 | Videos still processing; resume later |
| 4 | Video processing failure; inspect the report |

In macOS shells check `echo $?` immediately after the command; in PowerShell check `$LASTEXITCODE`. If pending and failed videos coexist, the command reports pending first, so always inspect the report too.

Keep OneDrive originals while validating the migration. Source deletion is outside this CLI's scope. See [protocol/recovery details](migration.md) and the [current plan](../tasks/plan.md) for remaining acceptance work.


If a source scan repeatedly stops, diagnose it without uploading or changing the saved job:

```bash
bash scripts/od2gp.sh source scan --diagnostics /
```

This displays rclone's bounded failure details in the terminal only. It may include folder/file names and provider details; review the output before sharing it. URLs and recognized credential fields are redacted. Default scan/run errors still use fixed sanitized categories. The scan does not silently skip unreadable source folders.


If diagnostics identify `Personal Vault: ... invalidResourceId: ObjectHandle is Invalid`, use the documented rclone workaround explicitly:

```bash
bash scripts/od2gp.sh source scan --exclude-personal-vault --diagnostics /
bash scripts/od2gp.sh run --job JOB_ID --exclude-personal-vault --wait
```

The second command is supported for a root job whose inventory is still incomplete. It saves the exclusion for later continuation and asks for upload approval after the scan completes. Personal Vault contents are not scanned or copied; their count and size remain unknown. The summary and reports record the excluded folder. Normal nested folders of the same name remain included. Completed inventories cannot have this scope changed; handle any Vault media separately. See [rclone's limitation and filter guidance](https://github.com/rclone/rclone/blob/master/docs/content/onedrive.md).

For a new drive-root (`/`) migration, or Continue on an incomplete root job without an exclusion, the menu asks how to handle Personal Vault. Choose **1** to exclude it, **2** to keep it included (listing may fail), or **0** to cancel. Excluded contents have unknown count and size and are not copied. The exclusion is saved on the same job; already fixed inventories keep their scope.

Before a new or unstarted job uploads, the menu warns about potentially overlapping saved jobs. Separate jobs do not share completed-file history. Enter `yes` only if you intend to proceed despite possible duplicates; otherwise use Continue on the original job. Cancelling keeps saved progress and starts no uploads.

To change parallelism for the same job, stop the running command with Ctrl+C, wait for exit, then run `bash scripts/od2gp.sh resume --job JOB_ID --wait --workers 3`. Use 1 for serial operation or up to 8; do not create another plan. See [parallel transfer behavior](migration.md#parallel-transfers).

Creation batches are enabled automatically for saved jobs. Use `--batch-size 1` to disable grouping for a diagnostic comparison, or the default `--batch-size 50`. Stop the old command before restarting the updated launcher on the same job. Batches flush after about two seconds, at the end of uploads or for staging pressure. Check HTTP status/operation in retry messages; 429 means rate limit or quota, whereas 5xx indicates a server error. Ambiguous creation outcomes retain staged bytes and require reviewed reconciliation. See [durable creation batches](migration.md#durable-creation-batches).

## Remembered transfer settings and guided recovery

Choose **8 — Transfer speed & options** to set speed once. Recommended resets to 3 parallel files, batches up to 50, five temporary retries and video waiting. Gentle changes parallelism to 1; Faster changes it to 6. These speed changes keep your other choices. Custom accepts 1-8 parallel files, batch size 1-50, retries 0-10 and whether to wait for video processing. Press Enter to keep a custom value, or type `b` to cancel without saving. Back/EOF/failed saving preserves your previous choices.

Preferences are saved in a separate private `menu.json` under the user od2gp configuration directory. It contains no account credentials. On the next launch, the menu displays and reuses these values automatically for New migration and Continue. Source scope, account bindings and staging cap remain those of the saved job. Explicit terminal commands retain their own flags/defaults.

A job with uncertain creation results is marked **needs recovery**. Choose Continue, select that job, then either review progress, explicitly retry uncertain items from verified original files, or go back. The retry choice applies to that run only; it is never saved as an automatic preference. Keep original encrypted staging and job keys. Ctrl+C exits with saved progress; reopen the menu and select Continue. No sign-in or media transfer starts simply from opening the menu or changing settings.
