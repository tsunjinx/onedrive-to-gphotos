# Windows verification and update plan

Reviewed 2026-10-31 against ee0611380483a23d66d53d9cfcea089b64a59a8e.

## Evidence and verdict

Not yet accepted for the full migration. Windows amd64 cross-build passed locally today. Native GitHub Windows Server 2025 runner successfully executed scripts/od2gp.ps1 version under PowerShell 7. The first-run setup test failed before invoking the launcher: `Go is still on PATH; first-run test would not exercise installation.` Consequently the following Go tests, vet and executable smoke steps did not run in that job. This is a CI fixture failure, not evidence that the application cannot run.

Run: https://github.com/tsunjinx/onedrive-to-gphotos/actions/runs/40228597360

The workflow removes only the directory of one resolved Go executable. It needs an isolated child-process PATH with every Go executable/shim removed, plus a precondition proving no Go is resolvable. Do not remove the assertion or mark failure as allowed.

## Ordered changes and exit gates

1. P0: Repair the no-Go test fixture and separate bootstrap from core Windows test jobs so bootstrap failure cannot hide core results. Enumerate all Go application resolutions, remove matching PATH directories in the child process, verify absence, install into a fresh directory containing spaces, then run version twice to verify reuse. Gate: installation and reuse pass, go test ./..., vet and binary version pass on the same commit.
2. P1: Add Windows PowerShell 5.1 and PowerShell 7 coverage. od2gp.cmd invokes powershell.exe, while current CI invokes pwsh. Test the real .cmd entry with isolated config and menu exit input. Exercise English/Vietnamese persistence and Unicode/space-containing paths. Gate: both shells and the desktop entry point pass without execution-policy bypass. Document user-controlled execution-policy/unblock requirements when applicable.
3. P1: Add native Windows bootstrap fault tests: missing/outdated/broken Go, checksum mismatch, offline download, incomplete installation, concurrent installation, build failure, argument forwarding and exit status. Existing Python bootstrap fault suite runs only on macOS. Gate: failures never start transfer and retries preserve valid installations.
4. P1: Test Windows-specific durability: exclusive job lock across processes, kill/reopen with partial encrypted download, completed-photo skip, pending-video polling, staging deletion after handles close, and resize refusal while running. Gate: no repeated confirmed creation; corrupt chunks fail closed; cap and disk reserve remain enforced.
5. P1 live acceptance on Windows 11 x64: dedicated test accounts; browser OAuth callback, Credential Manager storage and refresh after restart; mixed photo/video trial including a large video; Ctrl+C/resume and network interruption; inspect dates/orientation and video READY/playback/audio. No credentials in logs. Gate: reconcile eligible/skipped/failed counts with the report.
6. P2: Disposable Windows VM hard-power-off test with representative checkpoints, then throughput comparison at 1/3/6 workers before the 700 GB run. Gate: durable journal recovery and measured bounded staging under load.

## Scope

Windows x64 only is currently supported by the source launcher; ARM64 is explicitly refused. Cross-compilation does not validate Credential Manager, desktop execution policy, native file locking or power-loss recovery. The ordered changes above were the original review plan; implementation status follows below. Live acceptance is still separate.

## Implementation follow-up (2026-11-01)

- Bootstrap, core Go tests, and menu are independent CI jobs. Bootstrap and menu use both Windows PowerShell 5.1 and PowerShell 7, with 20-minute job deadlines.
- No-Go fixture removes all PATH entries exposing go.exe/go.cmd/go.bat. Real official download, checksum validation, installation in a path containing spaces, and reuse passed on both shells in run 41222251504.
- Offline fault injection covers missing, old and broken Go, failed download, bad checksum, partial managed installation, concurrent installer lock, and build failure. It exercises an isolated copy of the production launcher; it never disables checksum validation in production.
- Menu integration uses an isolated Unicode/space-containing config directory, saves Vietnamese then English, invokes the actual od2gp.cmd entry, and checks usage-error exit status. Input waits for the menu and uses raw bytes to avoid the .NET Framework UTF-8 BOM.
- The first native core test exposed POSIX-only permission assertions. Settings now apply a protected Windows DACL granting the current user and SYSTEM access; native tests inspect the directory/file ACL after first save and replacement. Unix mode checks remain enabled on Unix.
- Cross-process lock tests kill a lock holder and verify reacquisition. Staging tests kill an open writer, resume at the authenticated chunk boundary, verify the final plaintext hash, and delete the file to check handle cleanup. Existing native migration tests also cover completed-item skip, reopened pending-video polling, corrupt chunk rejection, and resize refusal while locked.
- User confirmed no Windows environment is currently available. Live OAuth, Credential Manager refresh with actual accounts, representative media playback, physical/VM power-off, and the 1/3/6-worker throughput trial remain open. Process termination is not evidence of power-loss durability.

See [Windows acceptance instructions](../docs/windows-acceptance.md) for the remaining live gate. Automated final run evidence is recorded below.

Additional findings fixed during native execution:

- Redirected Windows PowerShell input may contain a leading UTF-8 BOM. Prompt parsing now tolerates it; both shell menu jobs, language persistence and the CMD entry passed at 362f14a.
- Retry tests now bound the persisted deadline instead of assuming no time elapsed between persistence and sleep. The production retry schedule is unchanged.
- Source launcher suppresses PowerShell's expensive archive/download progress rendering while retaining explicit setup status/errors.

## Automated acceptance result

All 9 jobs passed on code commit `5a0a654f12cfc455a965c10a63a66d409e23f86b`:
https://github.com/tsunjinx/onedrive-to-gphotos/actions/runs/41224091382

- Native Windows Go tests, vet, launcher and binary smoke: PASS.
- Windows PowerShell 5.1 and PowerShell 7 offline bootstrap fault tests, real installation and reuse: PASS.
- Both shell menu integrations, Vietnamese/English persistence, Unicode config path, CMD entry and usage exit status: PASS.
- macOS tests, race checks, vet and bootstrap tests: PASS.
- Windows amd64 and macOS amd64/arm64 cross-builds: PASS.

Local full race suite and vet also passed; the revised retry-bound test passed 20 consecutive local runs. Later documentation-only commits do not alter the code tested above.

Automatable P0/P1 checks in this review are implemented. Remaining gates are Windows 11 desktop/account acceptance, Credential Manager refresh with real accounts, representative media and metadata inspection, VM power-off durability, and throughput measurement. No cloud transfer or account credential changes were performed during this review.
