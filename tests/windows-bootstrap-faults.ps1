# Offline fault injection against an isolated copy of the production launcher.
$ErrorActionPreference = 'Stop'
$hostExe = (Get-Process -Id $PID).Path
$repo = Split-Path $PSScriptRoot -Parent
$fixture = Join-Path $env:RUNNER_TEMP ('od2gp faults ' + [guid]::NewGuid().ToString('N'))
$scripts = New-Item -ItemType Directory -Path (Join-Path $fixture 'scripts')
Copy-Item (Join-Path $repo 'scripts\od2gp.ps1') $scripts.FullName
Copy-Item (Join-Path $repo 'scripts\toolchain.tsv') $scripts.FullName
$originalPath = $env:PATH
$originalTools = $env:OD2GP_TOOL_DIR
$cleanPath = (($originalPath -split ';') | Where-Object {
    $_ -and -not (Test-Path -LiteralPath (Join-Path $_ 'go.exe')) -and
    -not (Test-Path -LiteralPath (Join-Path $_ 'go.cmd')) -and
    -not (Test-Path -LiteralPath (Join-Path $_ 'go.bat'))
}) -join ';'
$fakeBin = New-Item -ItemType Directory -Path (Join-Path $fixture 'bin')
$wrapper = Join-Path $fixture 'invoke.ps1'
@'
$ErrorActionPreference = 'Stop'
function Invoke-WebRequest {
    param($Uri, $OutFile, $TimeoutSec, [switch]$UseBasicParsing)
    if ($env:OD2GP_TEST_FAULT -eq 'checksum') {
        [IO.File]::WriteAllText($OutFile, 'deliberately invalid archive')
    } else { throw 'offline fixture download refused' }
}
function Expand-Archive { throw 'FAULT: extraction must never run' }
. (Join-Path $PSScriptRoot 'scripts\od2gp.ps1') version
'@ | Set-Content -LiteralPath $wrapper -Encoding UTF8
$version = ((Get-Content (Join-Path $scripts.FullName 'toolchain.tsv') | Where-Object { $_ -match ' windows amd64 ' }) -split ' ')[0]
try {
    foreach ($case in @('missing', 'outdated', 'broken', 'checksum', 'partial', 'locked', 'build')) {
        $env:PATH = $fakeBin.FullName + ';' + $cleanPath
        $env:OD2GP_TOOL_DIR = Join-Path $fixture $case
        $env:OD2GP_TEST_FAULT = $case
        New-Item -ItemType Directory -Path $env:OD2GP_TOOL_DIR | Out-Null
        $fake = Join-Path $fakeBin.FullName 'go.cmd'
        if (Test-Path $fake) { Remove-Item -LiteralPath $fake }
        if ($case -eq 'outdated') { "@echo off`r`necho go version go1.0.0 windows/amd64`r`nexit /b 0" | Set-Content $fake -Encoding ASCII }
        if ($case -eq 'broken') { "@echo off`r`nexit /b 1" | Set-Content $fake -Encoding ASCII }
        if ($case -eq 'build') {
            "@echo off`r`nif `"%1`"==`"version`" (`r`necho go version go$version windows/amd64`r`nexit /b 0`r`n)`r`nexit /b 7" | Set-Content $fake -Encoding ASCII
        }
        if ($case -eq 'partial') { New-Item -ItemType Directory -Path (Join-Path $env:OD2GP_TOOL_DIR "go$version-windows-amd64") | Out-Null }
        $held = $null
        try {
            if ($case -eq 'locked') { $held = [IO.File]::Open((Join-Path $env:OD2GP_TOOL_DIR '.install-lock'), 'OpenOrCreate', 'ReadWrite', 'None') }
            # cmd redirects stderr so Windows PowerShell does not turn expected native errors into exceptions.
            $log = Join-Path $fixture ($case + '.log')
            & $env:ComSpec /d /c "`"`"$hostExe`" -NoProfile -File `"$wrapper`" > `"$log`" 2>&1`""
            if ($LASTEXITCODE -ne 1) { throw "$case did not fail closed" }
            $output = Get-Content -LiteralPath $log -Raw
            $expected = switch ($case) {
                'checksum' { 'checksum mismatch' }
                'partial' { 'Managed installation is invalid' }
                'locked' { 'Another setup is active' }
                'build' { 'Build failed' }
                default { 'offline fixture download refused' }
            }
            if ($output -notmatch [regex]::Escape($expected)) { throw "$case unexpected result: $output" }
            if ($output -match 'FAULT:') { throw 'Extraction ran on an unverified archive' }
            if (@(Get-ChildItem -LiteralPath $env:OD2GP_TOOL_DIR -Filter '.install-*' -Directory).Count -ne 0) { throw "$case left installation staging behind" }
            if (Test-Path (Join-Path $fixture '.cache')) {
                if (@(Get-ChildItem (Join-Path $fixture '.cache') -Filter 'launch-*').Count -ne 0) { throw "$case left build staging behind" }
            }
            Write-Output "PASS $case"
        } finally { if ($held) { $held.Dispose() } }
    }
} finally {
    $env:PATH = $originalPath
    $env:OD2GP_TOOL_DIR = $originalTools
    Remove-Item Env:OD2GP_TEST_FAULT -ErrorAction SilentlyContinue
}
exit 0
