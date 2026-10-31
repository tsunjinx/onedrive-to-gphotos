$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$hostExe = (Get-Process -Id $PID).Path
$env:OD2GP_CONFIG_DIR = Join-Path $env:RUNNER_TEMP ('od2gp menu ' + [char]0x1EC7 + ' ' + [guid]::NewGuid().ToString('N'))
$launcher = Join-Path $repo 'scripts\od2gp.ps1'
function Invoke-MenuProcess($Executable, $Arguments, $InputLines, $ExpectedExit = 0) {
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $Executable
    $info.Arguments = $Arguments
    $info.UseShellExecute = $false
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.StandardOutputEncoding = [Text.Encoding]::UTF8
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $info
    try {
        if (-not $process.Start()) { throw 'Process did not start' }
        $errors = $process.StandardError.ReadToEndAsync()
        if ($null -ne $InputLines) {
            # Wait for the actual menu: piping input before Go probing/building
            # lets earlier child commands consume it on Windows PowerShell.
            $buffer = New-Object char[] 1024
            $seen = ''
            while ($seen -notmatch '\]: ') {
                $read = $process.StandardOutput.ReadAsync($buffer, 0, $buffer.Length)
                if (-not $read.Wait(300000)) { throw 'Menu prompt timed out' }
                if ($read.Result -eq 0) { throw ('Menu ended before prompt: ' + $errors.Result) }
                $seen += [string]::new($buffer, 0, $read.Result)
            }
            # Write raw ASCII choices: .NET Framework's default StreamWriter can
            # prefix UTF-8 BOM, turning the first choice into an invalid command.
            $bytes = [Text.Encoding]::ASCII.GetBytes(($InputLines -join "`n") + "`n")
            $process.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
            $process.StandardInput.BaseStream.Flush()
        }
        $process.StandardInput.Close()
        $output = $process.StandardOutput.ReadToEndAsync()
        if (-not $process.WaitForExit(120000)) { throw 'Menu exit timed out' }
        Write-Output ($seen + $output.Result + $errors.Result)
        if ($process.ExitCode -ne $ExpectedExit) { throw "Unexpected exit $($process.ExitCode): $($errors.Result) $($output.Result)" }
    } finally {
        if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
        $process.Dispose()
    }
}
foreach ($language in @(@('2','vi'), @('1','en'))) {
    Invoke-MenuProcess $hostExe "-NoProfile -File `"$launcher`" menu" @('9',$language[0],'','0')
    $options = Get-Content -LiteralPath (Join-Path $env:OD2GP_CONFIG_DIR 'od2gp\menu.json') -Raw | ConvertFrom-Json
    if ($options.language -ne $language[1]) { throw 'Language was not saved.' }
}
Invoke-MenuProcess $env:ComSpec ('/d /c ""' + (Join-Path $repo 'od2gp.cmd') + '""') @('0')
Invoke-MenuProcess $hostExe "-NoProfile -File `"$launcher`" invalid-command" $null 2
exit 0
