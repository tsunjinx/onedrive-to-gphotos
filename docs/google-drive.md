# OneDrive → Google Drive (beta)

Copy every nonempty ordinary file and retain the complete folder tree, including empty folders. Exact source names are used. Zero-byte files are reported as `skipped_empty_file`; unknown sizes are errors. OneDrive originals are kept. Office files remain Office files; no archive packaging or Google Docs conversion occurs.

This is a new destination, separate from Google Photos. Its automated/fault tests do not establish live OAuth success, peak cloud throughput, or real power-loss safety on your machine. Run the small pilot below before selecting the entire drive.

## On the target Windows or Mac

Get the updated repository (Git pull, or download and extract the main-branch ZIP). Do not copy credentials or another computer's encrypted journals unless you also have a separately supported secure key migration; job keys live in that computer's OS credential store.

- Windows: double-click `od2gp.cmd`, or run `.\scripts\od2gp.ps1 menu` from PowerShell.
- macOS: double-click `od2gp.command`, or run `bash scripts/od2gp.sh menu`.

Launchers build the current source. Old manually downloaded executables do not acquire new functionality by pulling source.

Choose **11 — OneDrive → Google Drive**. In that submenu:

1. **Link accounts / Liên kết tài khoản**: guided provider setup and sign-in.
2. **Check connections / Kiểm tra kết nối**: identity refresh and Drive API/storage check.
3. **New migration / Tạo tác vụ mới**: pick the source drive/folder, new destination root name, staging cap and explicit Vault handling; review the plan and confirm upload.
4. **Continue / Tiếp tục**: choose the original Drive job after interruption. It retains its source drive and destination binding.
5. **Progress / Tiến trình**: inspect saved states and final-verification status.
6. **Export report / Xuất báo cáo**: save CSV without overwriting an existing local report. JSON is available from the CLI.
7. **Transfer options / Tùy chọn truyền**: `auto` or a fixed 1–8 workers. These choices are separate from Photos settings.
8. **Sign out / Đăng xuất tài khoản**: remove a local account credential before linking a different account. Saved jobs still require their original account.

A new job creates a new dedicated destination root. Do not create another migration merely to resume an interrupted one. Existing destination files are not overwritten or merged by name.

## Account setup

Passwords are entered only on the providers' browser pages. Login itself transfers no content. The wizard retains non-secret setup progress; a console step marked Done means you acknowledged it, not that the CLI obtained administrative access to verify that setting.

### Microsoft

Open https://entra.microsoft.com/ → App registrations. Use the existing configured public client if it is appropriate for your account, or register your own app supporting personal Microsoft accounts. Configure a Mobile/Desktop redirect URI of `http://localhost` and delegated Microsoft Graph `Files.Read`. This source is read-only; no Microsoft client secret or file-write permission is required by this desktop flow.

The wizard accepts a client ID, signs in, lists available drives and offers a contents preview. A tenant/account-type mismatch must be resolved in app registration/sign-in; repeatedly creating migration jobs does not fix it. The current native browser flow targets personal Microsoft accounts.

### Google

1. Select/create your Cloud project and enable Google Drive API at https://console.cloud.google.com/apis/library/drive.googleapis.com .
2. Configure Google Auth Platform Branding/Audience at https://console.cloud.google.com/auth/audience . Personal Gmail accounts use External. If the app is Testing, add the intended destination account as a test user.
3. Create a Desktop OAuth client at https://console.cloud.google.com/auth/clients and download JSON. Supply its path in the wizard; quote removal supports paths pasted from Windows.
4. Sign in for Google Drive and grant `https://www.googleapis.com/auth/drive.file`, plus identity scopes. Drive credentials use the separate `gdrive` keyring entry; an existing Photos sign-in does not grant Drive access.
5. Run Check connections and inspect the account/storage output before planning a migration.

For External/Testing apps, refresh tokens for Drive scopes normally expire after seven days. Review the app's publishing configuration before a long migration. Production status does not guarantee credentials cannot be revoked. Do not expand to full-drive access merely to bypass setup problems. Arbitrary existing-folder selection is not implemented in this beta; the application creates its own root.

If configuration is needed without the wizard, `OD2GP_DRIVE_CLIENT_FILE` selects a separate Desktop JSON. Otherwise Drive can reuse the configured Google Desktop JSON with a separate consent/session.

## What is preserved and what needs resolution

Folders, paths, names, original binary contents and supported modified timestamps are copied. Permissions/share links, version history and OneNote package exports are not portable in this flow. Shared shortcuts and inaccessible objects block inventory with an explicit error. Choosing an ordinary subfolder is an alternative scope; it does not mean excluded content was copied.

For a root scan, explicitly excluding `/Personal Vault` leaves its contents uncopied with unknown count/size. That choice remains in the job and reports. No hidden Photos media filters or Photos folder-upload exclusions are inherited.

Large files must fit the chosen encrypted staging cap and available disk space. Default staging is 30 GiB; choose a larger cap at planning if needed. The operating machine needs enough free space for encrypted file data plus the journal reserve. Overall Drive capacity and provider-specific limits still apply even with a large paid storage plan.

## Running unattended

Drive transfers use persisted delayed retries, rather than abandoning the job after five transient failures. Network/DNS/timeouts and temporary server failures retry with increasing delays. A temporary failure in one file or folder defers that item/subtree while independent work proceeds. Rate/quota responses respect server pauses. Reducing the worker count does not cancel healthy uploads.

Confirmed destination IDs are saved before cache deletion. Each file/folder has a pre-generated target ID, so a lost response is reconciled against the same object. Expired upload sessions restart only after checking the destination. Incomplete source downloads reuse authenticated encrypted chunks when supported. Keep the job directory and OS credential-store keys.

Permanent access denial, revoked consent, storage exhaustion, source changes or corrupt staging require intervention. The program cannot keep uploading through machine sleep, shutdown or local disk failure. Ctrl+C and later Continue preserve the original job; no auto-start at boot is installed.

Auto-workers starts at 3 within 1–8. It observes acknowledged chunk bytes and request latency over time windows, trials increases one at a time, backs off on failures/cache pressure and rolls back increases that do not help. Use a fixed count from submenu 7 if that is preferable. The progress bar uses approximate item-based ETA, which can vary with mixed file sizes and folder creation. Redirected output remains plain logs.

## Verification and reports

After transfer, verification checks destination IDs, exact names, parents, sizes, folder membership visible to the app, and a fresh source tree. New/moved/deleted/modified source items invalidate final verification; resolve or replan rather than assuming a snapshot of a changing drive.

Reports label `sha256` when the destination SHA-256 matches the hash calculated during download. If the server supplies no checksum, `metadata_only` is explicitly weaker and is not proof of identical bytes. No automatic full download from Google Drive is performed for verification. Upload session URLs and refresh credentials are not exported.

`completed` item states alone do not establish that the whole tree passed its final reconciliation. Inspect `VerifiedAt`, `FinalError`, unresolved states and exclusions in status/JSON. CSV records per-item results, an accepted Vault exclusion if present, and a final `job_verified`, `job_verified_with_exclusions` or `job_unverified` row.

## Optional CLI

```sh
bash scripts/od2gp.sh drive menu
bash scripts/od2gp.sh drive plan --source / --name "OneDrive Backup" --cache-gib 30 --exclude-personal-vault
bash scripts/od2gp.sh drive resume --job JOB_ID --workers auto
bash scripts/od2gp.sh drive status --job JOB_ID
bash scripts/od2gp.sh drive verify --job JOB_ID
bash scripts/od2gp.sh drive report --job JOB_ID --format json
```

On Windows replace `bash scripts/od2gp.sh` with `.\scripts\od2gp.ps1`. `--yes` suppresses the upload confirmation only when intentionally requested. `auth login/check/status/logout gdrive` manage the separate Drive credential.

## Acceptance on the other machine

- Prepare a small stable source folder containing nested/empty folders, one zero-byte file, Unicode names, Office/ZIP data, images and a video. Ensure at least one file requires multiple upload chunks.
- Link both accounts, verify identities, and plan into a new dedicated root. Compare the actual tree and report; the zero-byte file should be absent while its folder remains.
- Interrupt a larger upload with Ctrl+C, reopen the app and Continue the same job. Check target IDs and absence of duplicates. Test network disconnect/reconnect separately.
- Observe cache use and Auto-workers on mixed sizes. Compare a fixed-worker run on a separate test scope before making throughput claims.
- Use a disposable machine/VM for an abrupt-power-loss trial. Verify OS keys and journals survive; do not conduct a destructive trial against the only copy of needed local state.
- Record results, representative checksums and final-verification status, then run a medium trial before the whole drive.

Live account linking, native desktop behavior, sustained throughput and actual power-loss acceptance remain open until this evidence is supplied. These are not waived by passing CI.

## Setup troubleshooting

- Drive API `403 accessNotConfigured/serviceDisabled`: enable Drive API in the project belonging to the downloaded Desktop JSON, then Check connections again.
- Browser `access_denied`: confirm the intended destination is an allowed test user if Testing; check organization policy when applicable.
- Redirect mismatch: use a Desktop OAuth client JSON; Microsoft's desktop redirect must match the CLI loopback configuration. A Web client is not interchangeable.
- `insufficientPermissions`: reconnect Google Drive and grant the requested `drive.file` scope. Photos-only consent is insufficient.
- Revoked/expired consent: use Sign out and Link accounts for the same original account. Saved jobs cannot be resumed into a different account.
- `storageQuotaExceeded`: resolve destination storage before Continue. The paid plan alone does not prove available capacity.
