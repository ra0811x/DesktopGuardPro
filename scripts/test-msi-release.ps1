[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$script:releaseTestCalls = [System.Collections.Generic.List[string]]::new()
function go { $script:releaseTestCalls.Add('go'); $global:LASTEXITCODE = 0 }
function candle { $script:releaseTestCalls.Add('candle'); $global:LASTEXITCODE = 0 }
function light { $script:releaseTestCalls.Add('light'); $global:LASTEXITCODE = 0 }
$failure = $null
try {
    & (Join-Path $PSScriptRoot 'build-msi.ps1') -Version '9.9.9' -OutputDirectory '.msi-signing-test-must-not-be-created'
}
catch { $failure = $_ }
if ($script:releaseTestCalls.Count -ne 0) {
    throw "Packaging began without signing credentials: $($script:releaseTestCalls -join ', ')"
}
if ($null -eq $failure -or $failure.Exception.Message -notmatch 'CertificateThumbprint') {
    throw 'Missing signing credentials must fail before building or replacing any file.'
}

$source = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'build-msi.ps1') -Raw
foreach ($required in @('SignedReleaseDirectory', '--finalize', 'Get-AuthenticodeSignature', 'sign', 'verify', 'msi_signature_verified', 'Get-FileHash')) {
    if (-not $source.Contains($required)) { throw "Release pipeline is missing $required" }
}
if (-not $source.Contains('[IO.Directory]::Delete($buildRoot, $true)')) {
    throw 'Release pipeline must remove its private build directory after success or failure.'
}
$tokens = $null
$parseErrors = $null
$null = [System.Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw "Release script syntax errors: $parseErrors" }
Write-Output 'MSI signing gates and release script syntax verified.'

# Exercise the release order with local fixtures; no certificate or SDK is used.
$global:dgpReleaseTest = @{
    Thumbprint = '1234567890123456789012345678901234567890'
    Names = @('desktop-guard-service.exe', 'desktop-guard-ui.exe', 'desktop-guard-agent.exe', 'desktop-guard-maintenance.exe')
    RejectMSI = $false
    Calls = [System.Collections.Generic.List[string]]::new()
}
function Get-Item {
    param([string]$LiteralPath)
    if ($LiteralPath.StartsWith('Cert:\')) {
        return [pscustomobject]@{
            HasPrivateKey = $true; NotBefore = (Get-Date).AddDays(-1); NotAfter = (Get-Date).AddDays(1)
            EnhancedKeyUsageList = @([pscustomobject]@{ ObjectId = '1.3.6.1.5.5.7.3.3' })
        }
    }
    Microsoft.PowerShell.Management\Get-Item -LiteralPath $LiteralPath
}
function Get-AuthenticodeSignature {
    param([string]$LiteralPath)
    $signatureStatus = if ($global:dgpReleaseTest.RejectMSI -and $LiteralPath.EndsWith('.msi')) { 'NotSigned' } else { 'Valid' }
    [pscustomobject]@{ Status = $signatureStatus; SignerCertificate = [pscustomobject]@{ Thumbprint = $global:dgpReleaseTest.Thumbprint } }
}
function signtool { $global:dgpReleaseTest.Calls.Add(('signtool ' + ($args -join ' '))); $global:LASTEXITCODE = 0 }
function candle { $global:dgpReleaseTest.Calls.Add('candle'); $global:LASTEXITCODE = 0 }
function light {
    $global:dgpReleaseTest.Calls.Add('light')
    $target = $args[[Array]::IndexOf($args, '-out') + 1]
    [IO.File]::WriteAllText($target, 'mock MSI')
    $global:LASTEXITCODE = 0
}
function go {
    $global:dgpReleaseTest.Calls.Add(('go ' + ($args -join ' ')))
    $finalizeIndex = [Array]::IndexOf($args, '--finalize')
    if ($finalizeIndex -lt 0) { throw 'A pre-signed release must never be rebuilt.' }
    $directory = $args[$finalizeIndex + 1]
    foreach ($asset in @('desktop-guard-pro.ico', 'THIRD-PARTY-NOTICES.txt')) {
        if (-not (Test-Path -LiteralPath (Join-Path $directory $asset))) {
            throw "Pre-signed release asset was not copied: $asset"
        }
    }
    $components = @($global:dgpReleaseTest.Names | ForEach-Object {
        [pscustomobject]@{ fileName = $_; signature = 'trusted'; sha256 = (Get-FileHash -LiteralPath (Join-Path $directory $_)).Hash }
    })
    @{ productVersion = '9.9.9'; signingRequired = $false; components = $components } |
        ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $directory 'release-manifest.json')
    $global:LASTEXITCODE = 0
}
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('dgp-msi-test-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $fixtureRoot
try {
    $inputs = Join-Path $fixtureRoot 'signed inputs'
    $null = New-Item -ItemType Directory -Path $inputs
    foreach ($name in $global:dgpReleaseTest.Names) { [IO.File]::WriteAllText((Join-Path $inputs $name), "signed fixture $name") }
    [IO.File]::WriteAllText((Join-Path $inputs 'desktop-guard-ui.dll'), 'native UI runtime fixture')
    [IO.File]::WriteAllText((Join-Path $inputs 'desktop-guard-pro.ico'), 'icon fixture')
    [IO.File]::WriteAllText((Join-Path $inputs 'THIRD-PARTY-NOTICES.txt'), 'license fixture')
    '{"productVersion":"9.9.9"}' | Set-Content -LiteralPath (Join-Path $inputs 'release-manifest.json')
    $originalHashes = @($global:dgpReleaseTest.Names | ForEach-Object { (Get-FileHash -LiteralPath (Join-Path $inputs $_)).Hash })
    foreach ($reject in @($false, $true)) {
        $global:dgpReleaseTest.RejectMSI = $reject
        $global:dgpReleaseTest.Calls.Clear()
        $destination = Join-Path $fixtureRoot ([string]$reject)
        $failure = $null
        try {
            & (Join-Path $PSScriptRoot 'build-msi.ps1') -Version '9.9.9' -OutputDirectory $destination `
                -SignedReleaseDirectory $inputs -CertificateThumbprint $global:dgpReleaseTest.Thumbprint `
                -TimestampUrl 'http://timestamp.digicert.com'
        } catch { $failure = $_ }
        $artifact = Join-Path $destination 'DesktopGuardPro-9.9.9-windows-amd64.msi'
        if ($reject) {
            if ($null -eq $failure -or (Test-Path -LiteralPath $artifact)) { throw 'An untrusted MSI was published.' }
        } else {
            if ($failure) { throw $failure }
            $receipt = Get-Content -LiteralPath ($artifact + '.build.json') -Raw | ConvertFrom-Json
            if ($receipt.completedSteps[-1] -ne 'msi_signature_verified') { throw 'MSI verification receipt is missing.' }
            if ($receipt.msiSha256 -ne (Get-FileHash -LiteralPath $artifact).Hash) { throw 'MSI receipt hash mismatch.' }
            if (@($global:dgpReleaseTest.Calls | Where-Object { $_ -like 'go *' }).Count -ne 1) { throw 'Unexpected component rebuild.' }
            if (@($global:dgpReleaseTest.Calls | Where-Object { $_ -like 'signtool sign*.exe' }).Count -ne 0) { throw 'Pre-signed inputs were re-signed.' }
        }
    }
    $finalHashes = @($global:dgpReleaseTest.Names | ForEach-Object { (Get-FileHash -LiteralPath (Join-Path $inputs $_)).Hash })
    if (($originalHashes -join ',') -ne ($finalHashes -join ',')) { throw 'The signed input release was modified.' }
    Write-Output 'Pre-signed release reuse, finalization order, receipts, and failed-signature publication guards verified.'
}
finally {
    $resolvedFixture = [IO.Path]::GetFullPath($fixtureRoot)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolvedFixture.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($resolvedFixture) -notlike 'dgp-msi-test-*') { throw 'Unsafe fixture cleanup path.' }
    Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
    Remove-Variable -Name dgpReleaseTest -Scope Global
}
