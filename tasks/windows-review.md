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

Windows x64 only is currently supported by the source launcher; ARM64 is explicitly refused. Cross-compilation does not validate Credential Manager, desktop execution policy, native file locking or power-loss recovery. The above are proposed updates; this review does not change runtime code or claim live acceptance.
