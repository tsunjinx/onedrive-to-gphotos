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

```sh
bash scripts/od2gp.sh setup
bash scripts/od2gp.sh doctor
bash scripts/od2gp.sh auth login onedrive
bash scripts/od2gp.sh auth login google
```

Run `setup microsoft` once to replace an old Microsoft client ID. See [setup guide](docs/setup.md).

## Source inventory

```sh
bash scripts/od2gp.sh source scan /Pictures
```

Reads metadata and prints totals without uploading.

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
