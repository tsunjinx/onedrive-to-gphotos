$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$hostExe = (Get-Process -Id $PID).Path
$env:OD2GP_CONFIG_DIR = Join-Path $env:RUNNER_TEMP ('od2gp menu ' + [char]0x1EC7 + ' ' + [guid]::NewGuid().ToString('N'))
$launcher = Join-Path $repo 'scripts\od2gp.ps1'
# Choose Vietnamese then exit, and check persistence in an isolated config.
@('9','2','','0') | & $hostExe -NoProfile -File $launcher menu
if ($LASTEXITCODE -ne 0) { throw 'Language menu failed.' }
$options = Get-Content -LiteralPath (Join-Path $env:OD2GP_CONFIG_DIR 'od2gp\menu.json') -Raw | ConvertFrom-Json
if ($options.language -ne 'vi') { throw 'Language was not saved.' }
@('9','1','','0') | & $hostExe -NoProfile -File $launcher menu
if ($LASTEXITCODE -ne 0) { throw 'English menu failed.' }
$options = Get-Content -LiteralPath (Join-Path $env:OD2GP_CONFIG_DIR 'od2gp\menu.json') -Raw | ConvertFrom-Json
if ($options.language -ne 'en') { throw 'English was not saved.' }
'0' | & $env:ComSpec /d /c (Join-Path $repo 'od2gp.cmd')
if ($LASTEXITCODE -ne 0) { throw 'Desktop CMD menu entry failed.' }
& $hostExe -NoProfile -File $launcher invalid-command
if ($LASTEXITCODE -ne 2) { throw 'Launcher did not forward usage exit status.' }
exit 0
