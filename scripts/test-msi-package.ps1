[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'test-msi-command.ps1')
$projectRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$wixBin = 'C:\Program Files (x86)\WiX Toolset v3.14\bin'
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('dgp-msi-tables-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $fixtureRoot
try {
    foreach ($name in @('service', 'ui', 'agent', 'maintenance')) {
        [IO.File]::WriteAllText((Join-Path $fixtureRoot "desktop-guard-$name.exe"), "Non-executable MSI table fixture: $name")
    }
    [IO.File]::WriteAllText((Join-Path $fixtureRoot 'release-manifest.json'), '{}')
    $runtimeRoot = Join-Path $fixtureRoot 'native-ui-runtime'
    $runtimeThemeRoot = Join-Path $runtimeRoot 'Microsoft.UI.Xaml\Themes'
    $null = New-Item -ItemType Directory -Path $runtimeThemeRoot -Force
    [IO.File]::WriteAllText((Join-Path $runtimeRoot 'desktop-guard-ui.dll'), 'Native UI runtime fixture')
    [IO.File]::WriteAllText((Join-Path $runtimeThemeRoot 'generic.xaml'), '<ResourceDictionary />')
    $runtimeSource = Join-Path $fixtureRoot 'NativeUIRuntimeFiles.wxs'
    $heatOutput = & (Join-Path $wixBin 'heat.exe') dir $runtimeRoot -nologo -ag -sfrag -srd -sreg `
        -dr INSTALLFOLDER -cg NativeUIRuntimeFiles -var var.NativeRuntimeDirectory -out $runtimeSource 2>&1
    $heatOutput | Write-Output
    if ($LASTEXITCODE -ne 0) { throw 'WiX runtime harvesting failed.' }
    $object = Join-Path $fixtureRoot 'fixture.wixobj'
    $runtimeObject = Join-Path $fixtureRoot 'runtime.wixobj'
    $package = Join-Path $fixtureRoot 'NOT-FOR-INSTALLATION.msi'
    Push-Location (Join-Path $projectRoot 'installer')
    try {
        $mainCompileOutput = & (Join-Path $wixBin 'candle.exe') -nologo -arch x64 '-dProductVersion=9.9.9' "-dReleaseDirectory=$fixtureRoot" -out $object 'DesktopGuardPro.wxs' 2>&1
        $mainCompileOutput | Write-Output
        if ($LASTEXITCODE -ne 0) { throw 'WiX compilation failed.' }
        $runtimeCompileOutput = & (Join-Path $wixBin 'candle.exe') -nologo -arch x64 "-dNativeRuntimeDirectory=$runtimeRoot" -out $runtimeObject $runtimeSource 2>&1
        $runtimeCompileOutput | Write-Output
        if ($LASTEXITCODE -ne 0) { throw 'WiX runtime compilation failed.' }
        $compileOutput = @($mainCompileOutput) + @($runtimeCompileOutput)
        $compileOutput | Write-Output
        $linkOutput = & (Join-Path $wixBin 'light.exe') -nologo `
            -ext (Join-Path $wixBin 'WixUIExtension.dll') `
            -ext (Join-Path $wixBin 'WixUtilExtension.dll') `
            '-cultures:zh-CN' '-sice:ICE03' -out $package $object $runtimeObject 2>&1
        $linkOutput | Write-Output
        if ($LASTEXITCODE -ne 0) { throw 'MSI linking or ICE validation failed.' }
        if (($compileOutput -join "`n") -match 'warning LGHT' -or ($linkOutput -join "`n") -match 'warning LGHT') {
            throw 'WiX or ICE emitted a packaging warning.'
        }
    } finally { Pop-Location }

    $installer = New-Object -ComObject WindowsInstaller.Installer
    $database = $installer.OpenDatabase($package, 0)
    $sequence = @{}
    $view = $database.OpenView('SELECT `Action`, `Sequence` FROM `InstallExecuteSequence`')
    $view.Execute()
    while ($null -ne ($record = $view.Fetch())) { $sequence[$record.StringData(1)] = $record.IntegerData(2) }
    $view.Close()
    foreach ($pair in @(@('RollbackDesktopGuard', 'PrepareDesktopGuard'), @('PrepareDesktopGuard', 'InstallFiles'),
        @('InstallFiles', 'StopDesktopGuardRollback'), @('StopDesktopGuardRollback', 'ConfigureDesktopGuard'),
        @('ConfigureDesktopGuard', 'RemoveExistingProducts'), @('RemoveExistingProducts', 'InstallFinalize'))) {
        if (-not $sequence.ContainsKey($pair[0]) -or -not $sequence.ContainsKey($pair[1]) -or $sequence[$pair[0]] -ge $sequence[$pair[1]]) {
            throw "Compiled MSI ordering is wrong: $($pair -join ' before ')"
        }
    }
    $view = $database.OpenView('SELECT `Action`, `Type` FROM `CustomAction`')
    $view.Execute()
    $types = @{}
    while ($null -ne ($record = $view.Fetch())) { $types[$record.StringData(1)] = $record.IntegerData(2) }
    $view.Close()
    foreach ($name in @('PrepareDesktopGuard', 'ConfigureDesktopGuard', 'RollbackDesktopGuard', 'StopDesktopGuardRollback', 'CommitDesktopGuard', 'RemoveDesktopGuard')) {
        if (($types[$name] -band 2048) -eq 0 -or ($types[$name] -band 1024) -eq 0) { throw "Action lost deferred elevation: $name" }
    }
    if (($types['RollbackDesktopGuard'] -band 256) -eq 0 -or ($types['StopDesktopGuardRollback'] -band 256) -eq 0 -or ($types['CommitDesktopGuard'] -band 512) -eq 0) {
        throw 'Rollback or commit action type flags are missing.'
    }
    $view = $database.OpenView('SELECT `File`, `FileName`, `Version` FROM `File`')
    $view.Execute()
    $versions = @{}
    $fileNames = @{}
    while ($null -ne ($record = $view.Fetch())) {
        $versions[$record.StringData(1)] = $record.StringData(3)
        $fileNames[$record.StringData(1)] = $record.StringData(2)
    }
    $view.Close()
    foreach ($name in @('DesktopGuardService', 'DesktopGuardUI', 'DesktopGuardAgent', 'DesktopGuardMaintenance')) {
        if (-not $versions.ContainsKey($name) -or -not [string]::IsNullOrEmpty($versions[$name])) { throw "Unversioned file has an artificial MSI version: $name" }
    }
    if (-not $versions.ContainsKey('ThirdPartyNotices')) { throw 'MSI file table is missing THIRD-PARTY-NOTICES.txt.' }
    if (@($fileNames.Values | Where-Object { $_ -match '(^|\|)desktop-guard-ui\.dll$' }).Count -ne 1 -or
        @($fileNames.Values | Where-Object { $_ -match '(^|\|)generic\.xaml$' }).Count -ne 1) {
        throw 'MSI file table is missing native UI runtime files.'
    }
    $view = $database.OpenView('SELECT `File_` FROM `MsiFileHash`')
    $view.Execute()
    $hashed = @{}
    while ($null -ne ($record = $view.Fetch())) { $hashed[$record.StringData(1)] = $true }
    $view.Close()
    foreach ($name in @('DesktopGuardService', 'DesktopGuardUI', 'DesktopGuardAgent', 'DesktopGuardMaintenance')) {
        if (-not $hashed.ContainsKey($name)) { throw "Unversioned executable is missing from MsiFileHash: $name" }
    }

    $view = $database.OpenView("SELECT ``Type``, ``Property`` FROM ``Control`` WHERE ``Dialog_``='DesktopGuardOptionsDlg' AND ``Control``='DesktopShortcutCheckBox'")
    $view.Execute()
    $record = $view.Fetch()
    if ($null -eq $record -or $record.StringData(1) -ne 'CheckBox' -or $record.StringData(2) -ne 'CREATE_DESKTOP_SHORTCUT') {
        throw 'Compiled MSI is missing the desktop shortcut checkbox.'
    }
    $view.Close()
    $view = $database.OpenView("SELECT ``Event``, ``Argument``, ``Condition`` FROM ``ControlEvent`` WHERE ``Dialog_``='DesktopGuardOptionsDlg' AND ``Control_``='Next'")
    $view.Execute()
    $optionEvents = @()
    while ($null -ne ($record = $view.Fetch())) {
        $optionEvents += "$($record.StringData(1))|$($record.StringData(2))|$($record.StringData(3))"
    }
    $view.Close()
    if (-not ($optionEvents -match '^AddLocal\|DesktopShortcutFeature\|') -or -not ($optionEvents -match '^Remove\|DesktopShortcutFeature\|')) {
        throw 'Compiled MSI does not apply the desktop shortcut selection.'
    }
    $view = $database.OpenView("SELECT ``Argument``, ``Condition`` FROM ``ControlEvent`` WHERE ``Dialog_``='ExitDialog' AND ``Control_``='Finish' AND ``Event``='DoAction'")
    $view.Execute()
    $record = $view.Fetch()
    if ($null -eq $record -or $record.StringData(1) -ne 'LaunchDesktopGuardPro' -or $record.StringData(2) -notmatch 'NOT Installed') {
        throw 'Compiled MSI is missing the optional post-install launch action.'
    }
    $view.Close()
    $view = $database.OpenView("SELECT ``Action`` FROM ``InstallExecuteSequence`` WHERE ``Action``='LaunchDesktopGuardPro'")
    $view.Execute()
    if ($null -ne $view.Fetch()) { throw 'Silent installation must not run the completion-dialog launch action.' }
    $view.Close()
    Write-Output 'MSI compiled without WiX or ICE warnings; UI choices, elevated actions, rollback order, late product removal, and four executable hashes verified. No installation was performed.'
} finally {
    $record = $null; $view = $null; $database = $null; $installer = $null
    [GC]::Collect(); [GC]::WaitForPendingFinalizers()
    $resolvedFixture = [IO.Path]::GetFullPath($fixtureRoot)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolvedFixture.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolvedFixture) -notlike 'dgp-msi-tables-*') { throw 'Unsafe fixture cleanup path.' }
    Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
}
