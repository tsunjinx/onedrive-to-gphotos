# Windows acceptance

Supported launcher target: Windows x64. CI runs on Windows Server; it does not establish Windows 11 desktop/account acceptance. No local Windows test environment is currently available to the owner.

## Automated checks

Verified code: `5a0a654`, [9/9 CI jobs passed](https://github.com/tsunjinx/onedrive-to-gphotos/actions/runs/41224091382). Detailed findings and remaining gates: [Windows review](../tasks/windows-review.md).


GitHub Actions runs Go tests/vet and binary smoke on native Windows, plus separate Windows PowerShell 5.1 and PowerShell 7 jobs for bootstrap and menu. The tests use temporary settings and no cloud accounts. They cover encrypted checkpoint recovery using fake providers; no media is uploaded.

Settings use a protected Windows ACL for the current user and SYSTEM. Unix permission bits are not meaningful on Windows. OAuth refresh credentials remain in Windows Credential Manager; app settings contain client IDs and the path to the Google client file, not refresh tokens. Keep downloaded OAuth client files in your private user profile.

## Desktop trial when Windows is available

1. Use a disposable Windows 11 x64 VM or test machine and dedicated test cloud accounts. Clone the current repository into a path containing spaces. Start `od2gp.cmd`. The launcher should install verified Go locally if needed; an installed supported Go should be reused.
2. If Windows blocks a downloaded script, inspect its source and signature with `Get-AuthenticodeSignature .\scripts\od2gp.ps1` and inspect policy with `Get-ExecutionPolicy -List`. For a reviewed downloaded file, `Unblock-File .\scripts\od2gp.ps1` removes its downloaded-file marker. Follow administrator policy; do not bypass it or change machine policy automatically. Git clones usually do not have this marker.
3. Select language in menu 9, exit and reopen. Verify English and Vietnamese rendering and persisted choice. Check the same behavior in Windows PowerShell 5.1 and PowerShell 7.
4. Use menu 6 to configure/sign in, then menu 1 to check both accounts. Confirm source drive and destination identity. Restart Windows, check connections again, and repeat after access-token expiry to exercise refresh. Never paste tokens or credential-store contents into logs/issues.
5. Choose a small source folder containing a JPEG, HEIC, PNG, short video and a representative large video. Explicitly decide whether Personal Vault is excluded. Preview scope/counts, set an appropriate staging cap, and approve the displayed destination account.
6. During download/upload press Ctrl+C, reopen menu 3 and continue the **same job**. Repeat after a brief network interruption. Verify confirmed completed items are skipped, unfinished staging resumes where supported, and pending videos are polled until READY. Inspect dates, orientation, playback and audio in Google Photos. Export a report and reconcile eligible/completed/skipped/failed counts.
7. Attempt to continue the same job from a second terminal, and attempt to resize its cache while active. Both must refuse concurrent ownership. After the first process exits, retry successfully. Check staging use remains within cap and files can be cleaned up after handles close.

## Power loss and throughput

Only use a disposable VM and copies of test media. Force VM power-off separately during download, upload and creation/checkpoint persistence. Restart and continue the same job. A creation with an unknown outcome must require reconciliation rather than blind recreation; process-kill CI is not a substitute for this test.

Measure 1, 3 and 6 workers on disjoint representative folders to avoid separate-job duplicates. Record commit, Windows version, total bytes, elapsed time, effective MB/s, retries/429 responses and peak staging usage. Keep identical batching settings and account/network conditions. Do not infer 700 GB performance from small image counts alone. Approve the large migration only after the report and media checks pass.

Record date, commit, OS/shell version, report totals and pass/fail for each step. Keep account identifiers and file names private when sharing evidence.
