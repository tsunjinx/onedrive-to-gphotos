# Source launcher for Windows. Invoked with: & .\scripts\od2gp.ps1 help
# No parameter binding: forward every argument to the CLI unchanged.
$ErrorActionPreference = 'Stop'
$cliArgs = $args
$root = Split-Path $PSScriptRoot -Parent
$stage = $null
$buildDir = $null
$lockStream = $null
$oldToolchain = $env:GOTOOLCHAIN
$oldGoPath = $env:GOPATH
$oldGoCache = $env:GOCACHE
$result = 1
function Test-Go([string]$Executable, [version]$Required) {
    if (-not $Executable -or -not (Test-Path -LiteralPath $Executable -PathType Leaf)) { return $false }
    try {
        $output = & $Executable version 2>$null
        if ($LASTEXITCODE -ne 0 -or "$output" -notmatch '^go version go(\d+\.\d+\.\d+) windows/amd64$') { return $false }
        return ([version]$Matches[1] -ge $Required)
    } catch { return $false }
}
try {
    if ($env:OS -ne 'Windows_NT') { throw 'Use od2gp.sh on macOS.' }
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    if ($arch -ne 'AMD64') { throw 'This launcher currently supports Windows x64 only.' }
    $line = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'toolchain.tsv') | Where-Object { $_ -match '^\S+ windows amd64 ' } | Select-Object -First 1
    if (-not $line) { throw 'Missing Windows toolchain manifest entry.' }
    $fields = $line -split '\s+'
    $version, $platform, $target, $archive, $checksum = $fields
    if ($fields.Count -ne 5 -or $version -notmatch '^\d+\.\d+\.\d+$' -or $checksum -notmatch '^[a-f0-9]{64}$' -or $archive -ne "go$version.windows-amd64.zip") { throw 'Invalid toolchain manifest.' }
    $env:GOTOOLCHAIN = 'local'
    $required = [version]$version
    $goBin = $null
    $command = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { $goBin = $command.Source }
    $cache = $env:OD2GP_TOOL_DIR
    if (-not $cache) { $cache = Join-Path $env:LOCALAPPDATA 'od2gp\toolchains' }
    $managed = Join-Path $cache "go$version-windows-amd64"
    if (-not (Test-Go $goBin $required)) {
        $goBin = Join-Path $managed 'go\bin\go.exe'
        if (-not (Test-Go $goBin $required)) {
            New-Item -ItemType Directory -Force -Path $cache | Out-Null
            # FileShare.None serializes setup; the OS releases the handle on a crash.
            $lockPath = Join-Path $cache '.install-lock'
            try { $lockStream = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) }
            catch { throw 'Another setup is active or the tool directory is not writable; retry after it exits.' }
            if (-not (Test-Go $goBin $required)) {
                if (Test-Path -LiteralPath $managed) { throw 'Managed installation is invalid. Move it aside before retrying.' }
                $stage = Join-Path $cache ('.install-' + [guid]::NewGuid().ToString('N'))
                New-Item -ItemType Directory -Path $stage | Out-Null
                $zip = Join-Path $stage 'archive.zip'
                [Console]::Error.WriteLine("Installing Go $version locally for Windows x64...")
                $previousTLS = [Net.ServicePointManager]::SecurityProtocol
                try {
                    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
                    Invoke-WebRequest -UseBasicParsing -Uri "https://dl.google.com/go/$archive" -OutFile $zip -TimeoutSec 600
                } finally { [Net.ServicePointManager]::SecurityProtocol = $previousTLS }
                if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant() -ne $checksum) { throw 'Go archive checksum mismatch; refusing to extract or execute it.' }
                $unpacked = Join-Path $stage 'unpacked'
                Expand-Archive -LiteralPath $zip -DestinationPath $unpacked
                if (-not (Test-Go (Join-Path $unpacked 'go\bin\go.exe') $required)) { throw 'Downloaded toolchain failed validation.' }
                Move-Item -LiteralPath $unpacked -Destination $managed
            }
            $lockStream.Dispose()
            $lockStream = $null
        }
    }
    if (-not $env:GOPATH) { $env:GOPATH = Join-Path $root '.cache\gopath' }
    if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $root '.cache\go-build' }
    $buildDir = Join-Path $root ('.cache\launch-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $buildDir | Out-Null
    $binary = Join-Path $buildDir 'od2gp.exe'
    Push-Location $root
    try {
        & $goBin build -trimpath -o $binary ./cmd/od2gp
        if ($LASTEXITCODE -ne 0) { throw 'Build failed; the CLI was not started.' }
        & $binary @cliArgs
        $result = $LASTEXITCODE
    } finally { Pop-Location }
} catch {
    [Console]::Error.WriteLine("od2gp setup: $($_.Exception.Message)")
    [Console]::Error.WriteLine('Setup stopped before transfer. Check network access, writable disk space and toolchain configuration, then retry.')
} finally {
    if ($lockStream) { $lockStream.Dispose() }
    # These paths are unique directories generated by this invocation only.
    if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Recurse -Force }
    if ($buildDir -and (Test-Path -LiteralPath $buildDir)) { Remove-Item -LiteralPath $buildDir -Recurse -Force }
    $env:GOTOOLCHAIN = $oldToolchain
    $env:GOPATH = $oldGoPath
    $env:GOCACHE = $oldGoCache
}
exit $result
