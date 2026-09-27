[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^\d+(\.\d+){1,3}$')]
    [string]$Version,

    [string]$OutputDirectory = "releases\$Version",
    [string]$SignedReleaseDirectory,
    [string]$CertificateThumbprint,
    [ValidateSet('CurrentUser', 'LocalMachine')]
    [string]$CertificateStore = 'CurrentUser',
    [string]$TimestampUrl = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'

# Reject incomplete release inputs before compiling or touching a previous release.
if ($CertificateThumbprint -notmatch '^[0-9a-fA-F]{40}$') {
    throw 'A 40-character CertificateThumbprint for a trusted code-signing certificate is required.'
}
$timestampUri = $null
if (-not [Uri]::TryCreate($TimestampUrl, [UriKind]::Absolute, [ref]$timestampUri) -or $timestampUri.Scheme -notin @('http', 'https')) {
    throw 'TimestampUrl must be an absolute HTTP or HTTPS URL.'
}
$certificate = Get-Item -LiteralPath "Cert:\$CertificateStore\My\$CertificateThumbprint"
if (-not $certificate.HasPrivateKey -or $certificate.NotBefore -gt (Get-Date) -or $certificate.NotAfter -lt (Get-Date)) {
    throw 'The selected signing certificate must be valid and have an accessible private key.'
}
if (@($certificate.EnhancedKeyUsageList | Where-Object { $_.ObjectId -eq '1.3.6.1.5.5.7.3.3' }).Count -eq 0) {
    throw 'The selected certificate does not permit code signing.'
}

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$releaseRoot = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
} else { [IO.Path]::GetFullPath((Join-Path $projectRoot $OutputDirectory)) }
$buildRoot = Join-Path $releaseRoot ('.msi-build-' + [Guid]::NewGuid().ToString('N'))
$releaseDirectory = Join-Path $buildRoot "DesktopGuardPro-$Version-windows-amd64"
$wixSource = Join-Path $projectRoot 'installer\DesktopGuardPro.wxs'
$msiPath = Join-Path $releaseRoot "DesktopGuardPro-$Version-windows-amd64.msi"
$msiBuildPath = Join-Path $buildRoot "DesktopGuardPro-$Version-windows-amd64.msi"
if (Test-Path -LiteralPath $msiPath) { throw "Output already exists: $msiPath" }

$candleCommand = Get-Command candle -ErrorAction SilentlyContinue
$lightCommand = Get-Command light -ErrorAction SilentlyContinue
$heatCommand = Get-Command heat -ErrorAction SilentlyContinue
$wixBin = 'C:\Program Files (x86)\WiX Toolset v3.14\bin'
if ($null -eq $candleCommand -and (Test-Path -LiteralPath (Join-Path $wixBin 'candle.exe'))) {
    $candleCommand = Join-Path $wixBin 'candle.exe'
}
if ($null -eq $lightCommand -and (Test-Path -LiteralPath (Join-Path $wixBin 'light.exe'))) {
    $lightCommand = Join-Path $wixBin 'light.exe'
}
if ($null -eq $heatCommand -and (Test-Path -LiteralPath (Join-Path $wixBin 'heat.exe'))) {
    $heatCommand = Join-Path $wixBin 'heat.exe'
}
if ($null -eq $candleCommand -or $null -eq $lightCommand -or $null -eq $heatCommand) {
    throw 'WiX Toolset 3.14 is required. Install it with: winget install --id WiXToolset.WiXToolset --exact'
}
$wixUIExtension = Join-Path $wixBin 'WixUIExtension.dll'
$wixUtilExtension = Join-Path $wixBin 'WixUtilExtension.dll'
if (-not (Test-Path -LiteralPath $wixUIExtension) -or -not (Test-Path -LiteralPath $wixUtilExtension)) {
    throw 'WiX UI and Util extensions are required.'
}

$signTool = Get-Command signtool -ErrorAction SilentlyContinue
if ($null -eq $signTool) {
    $kitBin = 'C:\Program Files (x86)\Windows Kits\10\bin'
    if (Test-Path -LiteralPath $kitBin) {
        $signTool = Get-ChildItem -LiteralPath $kitBin -Directory | Sort-Object Name -Descending |
            ForEach-Object { Join-Path $_.FullName 'x64\signtool.exe' } |
            Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
}
if ($null -eq $signTool) { throw 'Windows SDK signtool.exe is required.' }

function Assert-ReleaseSignature([string]$Path) {
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $CertificateThumbprint) {
        throw "Invalid signature or unexpected publisher: $Path"
    }
}

function Invoke-ReleaseSigning([string]$Path) {
    $signArguments = @('sign', '/fd', 'SHA256', '/td', 'SHA256', '/tr', $TimestampUrl, '/sha1', $CertificateThumbprint)
    if ($CertificateStore -eq 'LocalMachine') { $signArguments += '/sm' }
    & $signTool @signArguments $Path
    if ($LASTEXITCODE -ne 0) { throw "Code signing failed: $Path" }
    Assert-ReleaseSignature $Path
}

function Get-CompatibleRelativePath([string]$BasePath, [string]$TargetPath) {
    $baseFullPath = [IO.Path]::GetFullPath($BasePath).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $targetFullPath = [IO.Path]::GetFullPath($TargetPath)
    $baseUri = New-Object Uri($baseFullPath)
    $targetUri = New-Object Uri($targetFullPath)
    return [Uri]::UnescapeDataString($baseUri.MakeRelativeUri($targetUri).ToString()).Replace('/', [IO.Path]::DirectorySeparatorChar)
}

$componentNames = @('desktop-guard-service.exe', 'desktop-guard-ui.exe', 'desktop-guard-agent.exe', 'desktop-guard-maintenance.exe')
$releaseAssetNames = @('desktop-guard-pro.ico', 'THIRD-PARTY-NOTICES.txt')
$completedSteps = [System.Collections.Generic.List[string]]::new()
$null = New-Item -ItemType Directory -Path $buildRoot -Force

Push-Location $projectRoot
try {
    if ($SignedReleaseDirectory) {
        $signedInput = (Resolve-Path -LiteralPath $SignedReleaseDirectory).Path
        $inputManifest = Get-Content -LiteralPath (Join-Path $signedInput 'release-manifest.json') -Raw | ConvertFrom-Json
        if ($inputManifest.productVersion -ne $Version) { throw 'Signed release version does not match Version.' }
        $null = New-Item -ItemType Directory -Path $releaseDirectory
        $signedEntries = @(Get-ChildItem -LiteralPath $signedInput -Recurse -Force)
        if (@($signedEntries | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count -ne 0) {
            throw 'Signed release input must not contain symbolic links or reparse points.'
        }
        foreach ($entry in Get-ChildItem -LiteralPath $signedInput -Force) {
            Copy-Item -LiteralPath $entry.FullName -Destination $releaseDirectory -Recurse
        }
        foreach ($name in $componentNames) { Assert-ReleaseSignature (Join-Path $releaseDirectory $name) }
        $completedSteps.Add('signed_inputs_copied_without_rebuild')
    }
    else {
        & go -C backend run ./cmd/desktop-guard-release --version $Version --output $buildRoot
        if ($LASTEXITCODE -ne 0) { throw "Release build failed with exit code $LASTEXITCODE." }
        $completedSteps.Add('components_built')
        foreach ($name in $componentNames) { Invoke-ReleaseSigning (Join-Path $releaseDirectory $name) }
    }
    foreach ($name in $componentNames) { Assert-ReleaseSignature (Join-Path $releaseDirectory $name) }
    $completedSteps.Add('component_signatures_verified')

    & go -C backend run ./cmd/desktop-guard-release --version $Version --finalize $releaseDirectory
    if ($LASTEXITCODE -ne 0) { throw 'Signed release finalization failed.' }
    $manifest = Get-Content -LiteralPath (Join-Path $releaseDirectory 'release-manifest.json') -Raw | ConvertFrom-Json
    if ($manifest.signingRequired -ne $false -or $manifest.components.Count -ne 4 -or $manifest.productVersion -ne $Version) {
        throw 'Finalized release manifest is incomplete.'
    }
    foreach ($name in $componentNames) {
        $entries = @($manifest.components | Where-Object { $_.fileName -eq $name })
        if ($entries.Count -ne 1 -or $entries[0].signature -ne 'trusted' -or
            (Get-FileHash -LiteralPath (Join-Path $releaseDirectory $name) -Algorithm SHA256).Hash -ne $entries[0].sha256) {
            throw "Finalized manifest does not match signed component: $name"
        }
    }
    $completedSteps.Add('signed_manifest_finalized')

    $nativeRuntimeDirectory = Join-Path $buildRoot 'native-ui-runtime'
    $null = New-Item -ItemType Directory -Path $nativeRuntimeDirectory
    $excludedRootFiles = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($name in @($componentNames + $releaseAssetNames + 'release-manifest.json')) { $null = $excludedRootFiles.Add($name) }
    $runtimeFileCount = 0
    foreach ($file in Get-ChildItem -LiteralPath $releaseDirectory -Recurse -File -Force) {
        if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw "Release runtime must not contain symbolic links or reparse points: $($file.FullName)"
        }
        $relativePath = Get-CompatibleRelativePath $releaseDirectory $file.FullName
        if (-not $relativePath.Contains([IO.Path]::DirectorySeparatorChar) -and $excludedRootFiles.Contains($relativePath)) {
            continue
        }
        $runtimeTarget = Join-Path $nativeRuntimeDirectory $relativePath
        $runtimeParent = Split-Path -Parent $runtimeTarget
        $null = New-Item -ItemType Directory -Path $runtimeParent -Force
        Copy-Item -LiteralPath $file.FullName -Destination $runtimeTarget
        $runtimeFileCount++
    }
    if ($runtimeFileCount -eq 0 -or -not (Test-Path -LiteralPath (Join-Path $nativeRuntimeDirectory 'desktop-guard-ui.dll'))) {
        throw 'Native UI runtime files are missing from the finalized release.'
    }
    $runtimeSource = Join-Path $buildRoot 'NativeUIRuntimeFiles.wxs'
    & $heatCommand dir $nativeRuntimeDirectory -nologo -ag -sfrag -srd -sreg -dr INSTALLFOLDER `
        -cg NativeUIRuntimeFiles -var var.NativeRuntimeDirectory -out $runtimeSource
    if ($LASTEXITCODE -ne 0) { throw "WiX runtime harvesting failed with exit code $LASTEXITCODE." }
    $completedSteps.Add('native_ui_runtime_harvested')

    Set-Location (Join-Path $projectRoot 'installer')
    $wixObject = Join-Path $buildRoot "DesktopGuardPro-$Version.wixobj"
    $runtimeObject = Join-Path $buildRoot 'NativeUIRuntimeFiles.wixobj'
    & $candleCommand -nologo -arch x64 "-dProductVersion=$Version" "-dReleaseDirectory=$releaseDirectory" -out $wixObject $wixSource
    if ($LASTEXITCODE -ne 0) {
        throw "WiX compilation failed with exit code $LASTEXITCODE."
    }
    & $candleCommand -nologo -arch x64 "-dNativeRuntimeDirectory=$nativeRuntimeDirectory" -out $runtimeObject $runtimeSource
    if ($LASTEXITCODE -ne 0) {
        throw "WiX runtime compilation failed with exit code $LASTEXITCODE."
    }

    & $lightCommand -nologo -ext $wixUIExtension -ext $wixUtilExtension '-cultures:zh-CN' '-sice:ICE03' -out $msiBuildPath $wixObject $runtimeObject
    if ($LASTEXITCODE -ne 0) {
        throw "MSI link failed with exit code $LASTEXITCODE."
    }
    $completedSteps.Add('msi_linked')
    Invoke-ReleaseSigning $msiBuildPath
    & $signTool verify /pa /all $msiBuildPath
    if ($LASTEXITCODE -ne 0) { throw 'MSI signature verification failed.' }
    $completedSteps.Add('msi_signature_verified')
    [ordered]@{
        productVersion = $Version
        createdUtc = [DateTime]::UtcNow.ToString('o')
        certificateThumbprint = $CertificateThumbprint
        inputDirectory = $releaseDirectory
        msiSha256 = (Get-FileHash -LiteralPath $msiBuildPath -Algorithm SHA256).Hash.ToLowerInvariant()
        completedSteps = @($completedSteps)
    } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath ($msiBuildPath + '.build.json') -Encoding utf8
    [IO.File]::Move($msiBuildPath, $msiPath)
    [IO.File]::Move(($msiBuildPath + '.build.json'), ($msiPath + '.build.json'))
}
finally {
    Pop-Location
    $resolvedBuildRoot = [IO.Path]::GetFullPath($buildRoot)
    $releasePrefix = [IO.Path]::GetFullPath($releaseRoot).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if ((Test-Path -LiteralPath $resolvedBuildRoot) -and
        $resolvedBuildRoot.StartsWith($releasePrefix, [StringComparison]::OrdinalIgnoreCase) -and
        [IO.Path]::GetFileName($resolvedBuildRoot) -like '.msi-build-*') {
        [IO.Directory]::Delete($buildRoot, $true)
    }
}

Write-Output $msiPath
