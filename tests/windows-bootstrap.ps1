# Native first-run integration test. Run only in disposable CI workspace.
$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$hostExe = (Get-Process -Id $PID).Path
$originalPath = $env:PATH
try {
    # Remove every PATH entry containing Go, including preinstalled runner versions.
    $env:PATH = (($originalPath -split ';') | Where-Object {
        $_ -and -not (Test-Path -LiteralPath (Join-Path $_ 'go.exe')) -and
        -not (Test-Path -LiteralPath (Join-Path $_ 'go.cmd')) -and
        -not (Test-Path -LiteralPath (Join-Path $_ 'go.bat'))
    }) -join ';'
    if (Get-Command go -CommandType Application -ErrorAction SilentlyContinue) {
        throw 'Go still resolves: first-run fixture is invalid.'
    }
    $env:OD2GP_TOOL_DIR = Join-Path $env:RUNNER_TEMP ('od2gp tools with spaces ' + [guid]::NewGuid().ToString('N'))
    $launcher = Join-Path $repo 'scripts\od2gp.ps1'
    & $hostExe -NoProfile -File $launcher version
    if ($LASTEXITCODE -ne 0) { throw 'First-run installation failed.' }
    $managed = Get-ChildItem -LiteralPath $env:OD2GP_TOOL_DIR -Filter go.exe -Recurse
    if ($managed.Count -ne 1) { throw 'Expected exactly one managed Go executable.' }
    $before = (Get-FileHash -LiteralPath $managed.FullName).Hash
    & $hostExe -NoProfile -File $launcher version
    if ($LASTEXITCODE -ne 0) { throw 'Managed toolchain reuse failed.' }
    if ((Get-FileHash -LiteralPath $managed.FullName).Hash -ne $before) { throw 'Reuse replaced the managed executable.' }
} finally { $env:PATH = $originalPath }
