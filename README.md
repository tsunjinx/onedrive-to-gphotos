# od2gp

Personal CLI for copying photos and videos **from OneDrive to Google Photos** on Windows and macOS. Original OneDrive files will remain untouched.

## Status

Setup, auth, plan/run/resume/status/report with encrypted progress, resumable uploads, bounded retries and video-readiness polling. Native Microsoft Graph client; existing rclone jobs keep their saved progress.

All Go tests, race checks, vet and cross-builds pass. Windows CI (bootstrap, PowerShell 5.1 and 7, menu, staging recovery) passes on GitHub Actions — see [Windows acceptance](docs/windows-acceptance.md). Live photo/video transfer and power-loss validation remain pending.

## Menu

Double-click **od2gp.command** (macOS) or **od2gp.cmd** (Windows), or run:

```sh
bash scripts/od2gp.sh menu   # macOS
.\scripts\od2gp.ps1 menu     # Windows PowerShell
```

Menu options: check connections · new migration · continue job · view progress · export report · reconnect accounts · change drive · transfer settings · language · source folder filters.

- **8 — Transfer speed**: Recommended (3 workers), Gentle (1), Faster (6), or Custom (1–8). Remembered between sessions.
- **9 — Language**: English or Tiếng Việt. Remembered separately from credentials.
- **10 — Source folder filters**: Exclude drive-root folders from new plans.

## Setup and sign in

Connect the **Microsoft account containing the OneDrive files** and the **Google account receiving the photos/videos**. Their email addresses may differ. Signing in does not start a transfer.

1. **OneDrive:** this project includes a registered public Microsoft Graph client. Sign in with the source account; no Azure subscription or Microsoft client secret is needed for this existing client. Use menu **7** to choose the drive and inspect its folders before saving the choice. If using your own Microsoft app, follow the [native Graph setup requirements](docs/setup.md#onedrive-native-microsoft-graph).
2. **Google:** create a project in [Google Cloud Console](https://console.cloud.google.com/), enable **Photos Library API**, and configure OAuth consent. For an app in Testing, add the destination Google account as a test user. Create an OAuth client of type **Desktop app**, download its JSON, and keep it in a private folder outside this repository. See [Google's app setup](https://developers.google.com/photos/overview/configure-your-app).
3. Open menu **6 — Set up or reconnect accounts**, supply the Google JSON path during setup, and sign in to both accounts. Then use menu **1 — Check both account connections**. Verify the displayed source and destination identities before starting a migration.

If both accounts already pass their connection checks, you can continue your saved migration without recreating the OAuth apps. To switch accounts, explicitly log out of the relevant provider first; existing jobs retain their account bindings.

Equivalent macOS commands (Windows: replace `bash scripts/od2gp.sh` with `.\scripts\od2gp.ps1`):

```sh
bash scripts/od2gp.sh setup
bash scripts/od2gp.sh doctor
bash scripts/od2gp.sh auth login onedrive
bash scripts/od2gp.sh auth login google
bash scripts/od2gp.sh auth check onedrive
bash scripts/od2gp.sh auth check google
```

Run `setup microsoft` once to replace an old Microsoft client ID. Google refresh tokens for external apps in Testing generally expire after seven days with these scopes; reauthorize and continue the same job when needed. Paid Google storage does not change OAuth lifetimes or API quotas. See [setup guide](docs/setup.md).

## Source inventory

```sh
bash scripts/od2gp.sh source scan /Pictures
```

Reads metadata and prints totals without uploading.

Saved **Graph migrations of `/`** use delta pages instead of visiting every folder separately. Each page and its continuation are encrypted and committed together. Continue reuses saved pages after interruption; uploads remain blocked until the complete inventory is validated. The terminal shows page timings and a 10-second heartbeat while waiting for Graph, including retry pauses.

Old incomplete jobs need one fresh scan because their previous file counter was not a traversal checkpoint. Folder-only jobs, rclone jobs and standalone `source scan` retain the children/listing scanner. See [scan recovery and update instructions](docs/runbook.md#graph-inventory-prefetch).

## Migration

```sh
bash scripts/od2gp.sh plan --source /Pictures/Test --cache-gib 30
bash scripts/od2gp.sh run --job JOB_ID --wait
bash scripts/od2gp.sh resume --job JOB_ID --wait
```

Retries up to 5× per file by default (`--retries 0` to disable). Workers default to 3 (`--workers 1-8`). Batch creation up to 50 items (`--batch-size 1-50`). To grow a saved staging limit: `resize-cache --job JOB_ID --cache-gib N`.

Do not assume a new plan resumes an old job. See [migration guide](docs/migration.md) and [runbook](docs/runbook.md).

## Build

```sh
go test ./...
go build -o dist/od2gp ./cmd/od2gp
```

Windows: `go build -o dist/od2gp.exe ./cmd/od2gp`. The launcher auto-installs Go (1.27.2+) if needed; no admin access required.

See [plan](tasks/plan.md) · [todo](tasks/todo.md) · [platform policy](docs/platform-policy-review.md).

Confirmed media creation automatically releases that file's encrypted staging cache, including videos still processing in Google Photos. Byte-upload completion alone retains staging until media creation is confirmed and journaled. Do not delete saved-job databases or manually clear the staging folder during a migration.

Uploads continue while a single background committer creates media batches, within the shared staging cap. Video status polling follows the transfer pass. Progress includes download/version-check duration, upload-session duration, batch flush reason/queue age and a staging reservation summary.
