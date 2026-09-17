[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ExecutablePath,

    [string]$ExpectedVersion
)

$ErrorActionPreference = 'Stop'

$resolvedExecutable = (Resolve-Path -LiteralPath $ExecutablePath).Path
if ($ExpectedVersion) {
    $versionInfo = (Get-Item -LiteralPath $resolvedExecutable).VersionInfo
    $actualFileVersion = [Version]$versionInfo.FileVersion
    if ($actualFileVersion.ToString(3) -ne $ExpectedVersion -or $versionInfo.ProductVersion -ne $ExpectedVersion) {
        throw "Native UI versions are file=$($versionInfo.FileVersion), product=$($versionInfo.ProductVersion); expected $ExpectedVersion."
    }
}
$sdkBin = 'C:\Program Files (x86)\Windows Kits\10\bin'
$manifestTool = Get-ChildItem -LiteralPath $sdkBin -Recurse -Filter mt.exe -ErrorAction SilentlyContinue |
    Where-Object { $_.DirectoryName -like '*\x64' } |
    Sort-Object FullName -Descending |
    Select-Object -First 1 -ExpandProperty FullName
if (-not $manifestTool) {
    throw 'Windows SDK manifest tool mt.exe is required.'
}

$manifestPath = [IO.Path]::GetTempFileName()
try {
    & $manifestTool "-inputresource:$resolvedExecutable;#1" "-out:$manifestPath"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to extract the application manifest from $resolvedExecutable"
    }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw
    $fileNames = [regex]::Matches($manifest, '<asmv3:file name="([^"]+)"') |
        ForEach-Object { $_.Groups[1].Value }
    $duplicates = @($fileNames | Group-Object | Where-Object Count -gt 1)
    if ($duplicates.Count -gt 0) {
        $summary = ($duplicates | Sort-Object Name | ForEach-Object { "$($_.Name) x$($_.Count)" }) -join ', '
        throw "Application manifest contains duplicate runtime file entries: $summary"
    }

    $startedAt = Get-Date
    $process = Start-Process -FilePath $resolvedExecutable -PassThru
    try {
        if ($process.WaitForExit(10000)) {
            throw "Native UI exited during the launch probe with code $($process.ExitCode)."
        }
        $sideBySideFailure = Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'SideBySide'; StartTime = $startedAt } -ErrorAction SilentlyContinue |
            Where-Object { $_.Message -like "*$resolvedExecutable*" } |
            Select-Object -First 1
        if ($sideBySideFailure) {
            throw "Native UI produced SideBySide event $($sideBySideFailure.Id): $($sideBySideFailure.Message)"
        }
    }
    finally {
        if ($process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force
            $process.WaitForExit()
        }
    }
}
finally {
    if (Test-Path -LiteralPath $manifestPath) {
        Remove-Item -LiteralPath $manifestPath -Force
    }
}

Write-Output 'Native UI manifest and launch probe passed.'
