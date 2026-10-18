# Personal account setup

Status: guided OAuth setup and browser login are implemented. The owner reports both account checks working; native Windows/fresh-machine checks remain pending. If both checks already pass, proceed to the [operating instructions](runbook.md). Do not paste passwords, tokens or downloaded credentials into chat. Keep the Google client file outside this repository.

## OneDrive (native Microsoft Graph)

New jobs use direct Microsoft Graph requests. This personal project's default public client ID is `dd4ceec5-81a2-4c43-b4f7-5680436926aa`. Microsoft credentials are separate from the old rclone connection; sign in again through the new app.

For this machine, replace any older saved Microsoft client ID while preserving the Google settings:

```sh
bash scripts/od2gp.sh setup microsoft dd4ceec5-81a2-4c43-b4f7-5680436926aa
bash scripts/od2gp.sh doctor
bash scripts/od2gp.sh auth login onedrive
bash scripts/od2gp.sh source check
bash scripts/od2gp.sh source select-drive
bash scripts/od2gp.sh source scan --exclude-personal-vault /
```

Windows uses `.\scripts\od2gp.ps1` with the same arguments. The registered app must support personal Microsoft accounts, have a **Mobile and desktop applications** redirect URI `http://localhost`, and permit delegated **Files.Read**. The CLI requests `openid email offline_access` as well as Files.Read and uses browser authorization with PKCE. No Microsoft client secret is used. Environment `OD2GP_MICROSOFT_CLIENT_ID` overrides saved/project defaults. If a different account/client is already saved under the native credential, explicitly run `auth logout onedrive` before signing in with the replacement; this does not delete the legacy rclone credential.

`source select-drive` shows account drives and a metadata-only folder/media preview before saving the selected drive. If no choice has been saved, native commands use `/me/drive`, never an arbitrary metadata drive. Root inventory requires an explicit `--exclude-personal-vault` when Vault is present; its inaccessible contents remain outside the selected scope. Native shared/remote shortcuts are not followed silently. Prefer an ordinary photo folder for the first native trial.

The native adapter uses paginated children listings, stable IDs and eTag/cTag version metadata. Downloads stream into encrypted staging. Refresh credentials and the selected drive ID are stored in macOS Keychain or Windows Credential Manager; access tokens remain in memory. Preauthenticated download URLs are used by an anonymous HTTPS client without the Graph bearer token. [Children API](https://learn.microsoft.com/en-us/graph/api/driveitem-list-children), [downloads](https://learn.microsoft.com/en-us/graph/api/driveitem-get-content).

## Existing rclone jobs and fallback

Jobs created before native Graph remain rclone jobs, including `84f8505d28b7ff00c8aab99f1c69533d`. Continue those IDs as before; the job selects its recorded backend automatically. Do not create an overlapping new Graph plan to continue an old job: the completion histories are independent and source version metadata differs.

For explicit legacy login, drive selection or scans on macOS:

```sh
OD2GP_SOURCE_BACKEND=rclone bash scripts/od2gp.sh auth login onedrive
OD2GP_SOURCE_BACKEND=rclone bash scripts/od2gp.sh source select-drive
```

PowerShell: set `$env:OD2GP_SOURCE_BACKEND = 'rclone'` before the command and remove that override afterward when returning to Graph. Legacy tokens stay in their dedicated encrypted rclone config; its encryption password remains in the OS credential store. Existing source settings are retained. No migration history or credential is converted automatically.

## Google

1. Create a Google Cloud project and enable the Photos Library API.
2. Configure the OAuth consent screen for personal use. If using Testing status, add your destination Google account as a test user.
3. Create an OAuth client of type **Desktop app** and download its JSON file to a private folder outside the repository. Keep the supplied client ID and client secret together; the desktop secret is not a substitute for PKCE.
4. The login requests `photoslibrary.appendonly` and `photoslibrary.readonly.appcreateddata` for uploads and video processing checks, plus `openid email` for stable destination-account identification.
5. Google refresh tokens issued to external apps in Testing generally expire after seven days with these scopes. The CLI must preserve progress and request reauthorization when necessary. Test refresh behavior before a multi-day migration; do not assume that a paid Google storage plan changes OAuth lifetimes or API quotas.

[Desktop OAuth](https://developers.google.com/identity/protocols/oauth2/native-app), [token expiration](https://developers.google.com/identity/protocols/oauth2/), [app-created media access](https://developers.google.com/photos/library/guides/access-media-items).

## One-time guided setup

Run this from the project folder on macOS or Windows:

```sh
bash scripts/od2gp.sh setup
```

Windows PowerShell:

```powershell
.\scripts\od2gp.ps1 setup
```

The wizard opens Google Cloud and prompts for the path to your downloaded Google Desktop OAuth JSON. The JSON stays where you downloaded it; its absolute path is saved in your user config directory. OneDrive uses native Graph browser login and is run separately. Setup preserves saved Microsoft settings. Setup never prints or copies the Google client secret. You can rerun `setup` to replace the settings; invalid input leaves existing settings intact.

After setup:

```sh
bash scripts/od2gp.sh doctor
```

`doctor` checks local configuration only. It does not sign in or contact either provider. Environment variables `OD2GP_MICROSOFT_CLIENT_ID` and `OD2GP_GOOGLE_CLIENT_FILE` can still override saved values for advanced use. `OD2GP_CONFIG_DIR` chooses an alternate settings directory.

## Browser login

```sh
bash scripts/od2gp.sh auth login onedrive
bash scripts/od2gp.sh auth login google
```

Read the in-terminal privacy notice and type `yes`. The system browser opens; choose the intended account and grant access. Return to the terminal for the result. No transfer starts. Use `auth check onedrive` and `auth check google` to verify offline refresh after signing in. `auth status` and `auth logout` also require a provider argument.

Both native browser logins use PKCE, state validation and a five-minute local callback window. Account identity is checked through trusted provider discovery/UserInfo over HTTPS; unverified token claims are not used as identity proof. Legacy rclone authorization remains available for older jobs.

Secure storage is tested before browser login. Only the compact refresh credential, client ID and account identity are saved; access tokens remain in memory. The macOS adapter uses the system Keychain utility with secrets sent through stdin. Oversized credentials fail closed because native stores have size limits. No plaintext fallback is provided. If a different account/client is already saved, explicitly log out before switching.

Local logout does not revoke remote consent. Revoke Microsoft access at https://account.live.com/consent/Manage and Google access at https://myaccount.google.com/connections. See `od2gp privacy` and [the privacy notice](privacy.md).
