# od2gp

Personal CLI for copying photos and videos **from OneDrive to Google Photos** on Windows and macOS. Original OneDrive files will remain untouched.

## Current status

The CLI provides setup and authentication, plus plan/run/resume/status/report with encrypted saved progress, resumable uploads, bounded retries and video-readiness polling. New jobs use native Microsoft Graph with the registered personal-project public client; existing rclone jobs keep their source backend and saved progress. Run `setup microsoft` once to replace any old saved Microsoft client ID, then sign in again for native Graph. The owner has tested legacy account connections and started a legacy transfer; the owner has now completed a native Graph inventory and started uploading photos; parallel throughput still needs live verification. Live photo/video transfer, native Windows execution and actual power-loss validation remain pending.

Go tests, race checks, vet and cross-builds for Windows x64 and both macOS architectures pass. The ten offline bootstrap tests pass. Follow the [step-by-step operating instructions](docs/runbook.md) for the trial, full migration and recovery; see the [updated plan](tasks/plan.md) for remaining acceptance gates.

## Start with the interactive menu

From the project directory:

```sh
bash scripts/od2gp.sh menu
```

Windows PowerShell:

```powershell
.\scripts\od2gp.ps1 menu
```

Choose a number to check both connections, preview a new migration, continue a saved job, view progress, export a report or reconnect accounts. **2** asks which OneDrive drive to use before asking for its folder; **7** changes the selected drive directly. Drive choices show numbered names and mark the current drive. After choosing one, review its folders and sample photo/video filenames: **1** uses it, **2** tries another drive, **3** inspects a folder and **0** cancels without changing the saved drive. Saved jobs show their folder and progress; you do not need to copy job IDs. Press Enter to accept the suggested choice. New jobs still ask for confirmation before uploading. Continuing a job uses three parallel files, video polling and the default five retries per file.

Launching without arguments also opens the menu when both input and output are a terminal. Redirected/piped no-argument invocations retain help/list output. Existing commands remain available below.

## Build and check

Run from source; the launcher automatically installs Go if needed:

```sh
# macOS
bash scripts/od2gp.sh help
```

```powershell
# Windows PowerShell
.\scripts\od2gp.ps1 help
```

Installation uses official archives pinned with SHA-256 checksums in `scripts/toolchain.tsv`. The bootstrap accepts Go 1.27.2 or newer and installs 1.27.2 when needed. No administrator access or permanent PATH changes. A first download requires internet access. Windows execution policy may require unblocking locally reviewed downloaded scripts; the launcher does not bypass machine policy.

Tools are cached under `~/Library/Caches/od2gp/toolchains` on macOS or `%LOCALAPPDATA%\od2gp\toolchains` on Windows. Override with `OD2GP_TOOL_DIR`. A broken managed installation is reported for manual inspection, never overwritten. Interrupted macOS setup can leave an empty `.install-lock`; remove that lock only after confirming no setup process is running.

For direct development with an installed compatible [Go release](https://go.dev/dl/) (language minimum 1.26, required by the OAuth dependency):

```sh
go test ./...
go vet ./...
go build -o dist/od2gp ./cmd/od2gp
./dist/od2gp help
```

On Windows, build with `go build -o dist/od2gp.exe ./cmd/od2gp` and run `.\dist\od2gp.exe help`. Run `go test -race ./...` on macOS; Windows race testing additionally requires a compatible C toolchain.

[Guided setup](docs/setup.md) opens Google Cloud and validates the Google Desktop OAuth file. OneDrive signs in through rclone's registered client. It saves the settings privately so you do not need shell environment variables. `doctor` checks the saved configuration locally without contacting either provider.

Transfers require your computer to stay awake, online and running the CLI. Reopen it with the same saved job ID to resume interruptions. Your reported Google storage allowance is 5 TB; remaining free space is separate from API request quotas.

See the [reviewed plan](tasks/plan.md), [review findings](tasks/review.md), and [implementation checklist](tasks/todo.md).

Read the [platform policy review](docs/platform-policy-review.md) for the requirements that will gate live account access.

## Sign in

Create the Google OAuth client with guided [setup](docs/setup.md), then connect OneDrive through rclone:

```sh
bash scripts/od2gp.sh setup
bash scripts/od2gp.sh doctor
bash scripts/od2gp.sh auth login onedrive
bash scripts/od2gp.sh auth login google
bash scripts/od2gp.sh auth check onedrive
bash scripts/od2gp.sh auth check google
```

Each login shows a privacy notice and requires typing `yes` before opening the browser. `auth status <provider>` reads the saved account locally; `auth check <provider>` refreshes access and verifies that the account still matches. `auth logout <provider>` deletes local credentials; use the displayed provider URL to revoke the remote grant. Login does not transfer media.

On Windows, use `.\scripts\od2gp.ps1 setup` followed by `.\scripts\od2gp.ps1 auth login onedrive` and `.\scripts\od2gp.ps1 auth login google`. Source launchers use project-local `.cache/gopath` and `.cache/go-build` by default; explicit GOPATH/GOCACHE settings are respected. Initial dependency downloads also require internet access.

## Source inventory

```sh
bash scripts/od2gp.sh source scan /Pictures
```

This reads metadata and prints file/byte totals without uploading. The approximately 700 GB library does not need to be downloaded for inventory. The transfer engine now saves progress in an encrypted SQLite journal and uses bounded encrypted staging. See the migration guide before beginning a transfer; real power-loss testing is still pending.

Rclone is a runtime dependency; the CLI installs a pinned official executable if it is absent. No Azure registration is required. Source configuration is encrypted separately from your existing rclone remotes. Source commands hold an OS lock that releases when the process exits or dies. The owner reports successful Microsoft browser login; fresh-machine/native Windows checks remain pending.

## Start a migration

Begin with a small folder containing both a photo and a video:

```sh
bash scripts/od2gp.sh plan --source /Pictures/Test --cache-gib 30
bash scripts/od2gp.sh run --job JOB_ID --wait
```

Use the generated job ID from the plan. Temporary Google failures receive up to five retries per file with increasing delays and saved provider pauses; `--retries 0` disables automatic retries. After interruption:

```sh
bash scripts/od2gp.sh resume --job JOB_ID --wait
```

Read [migration and recovery instructions](docs/migration.md) for pending video processing, explicit uncertain-result reconciliation, reports and current limitations. Do not assume a new plan resumes an old job.

For a new drive-root (`/`) migration, or Continue on an incomplete root job without an exclusion, the menu asks how to handle Personal Vault. Choose **1** to exclude it, **2** to keep it included (listing may fail), or **0** to cancel. Excluded contents have unknown count and size and are not copied. The exclusion is saved on the same job; already fixed inventories keep their scope.

Before a new or unstarted job uploads, the menu warns about potentially overlapping saved jobs. Separate jobs do not share completed-file history. Enter `yes` only if you intend to proceed despite possible duplicates; otherwise use Continue on the original job. Cancelling keeps saved progress and starts no uploads.

Transfers default to three parallel files, sharing the saved staging cap; Google library creation stays sequential. To change the worker count without changing jobs, stop the existing run, then use `bash scripts/od2gp.sh resume --job JOB_ID --wait --workers 3` (range 1–8). See [transfer details](docs/migration.md#parallel-transfers).

Creation now uses one sequential batch committer, with up to 50 items per request (`--batch-size 1-50`, default 50). Token queues are saved in the encrypted journal and flush early for queue time, staging pressure or the end of uploads. Retry diagnostics identify HTTP status and operation. See [durable batches](docs/migration.md#durable-creation-batches).
