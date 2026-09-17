[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
[xml]$wix = Get-Content -Raw -Encoding UTF8 (Join-Path $projectRoot 'installer\DesktopGuardPro.wxs')
$namespaces = [System.Xml.XmlNamespaceManager]::new($wix.NameTable)
$namespaces.AddNamespace('wix', 'http://schemas.microsoft.com/wix/2006/wi')
$action = $wix.SelectSingleNode('//wix:CustomAction[@Id="ConfigureDesktopGuard"]', $namespaces)
$removeAction = $wix.SelectSingleNode('//wix:CustomAction[@Id="RemoveDesktopGuard"]', $namespaces)
if ($null -eq $removeAction -or $removeAction.Execute -ne 'deferred' -or $removeAction.Impersonate -ne 'no' -or
    $removeAction.ExeCommand -notmatch '--owner-sid "\[UserSID\]"' -or
    $removeAction.ExeCommand -notmatch '--msi-managed') {
    throw 'MSI uninstall must run elevated, forward the installing user SID, and leave program-file removal to Windows Installer.'
}

if ($null -eq $action) {
    throw 'ConfigureDesktopGuard is missing from the MSI definition.'
}

if ($action.ExeCommand -notmatch '--install-dir "\[INSTALLFOLDER\]\."') {
    throw 'The install directory must end with a dot so the quoted command line does not escape its closing quote.'
}

if ($action.ExeCommand -notmatch 'msi --stage apply' -or $action.ExeCommand -match '--skip-service-health-check') {
    throw 'MSI must apply the prepared transaction and require a successful service health probe.'
}

$upgrade = $wix.SelectSingleNode('//wix:MajorUpgrade', $namespaces)
if ($upgrade.Schedule -ne 'afterInstallExecute') { throw 'MSI must retain the installed product until the new files and deferred transaction have executed.' }
foreach ($entry in @(
    @('RollbackDesktopGuard', 'rollback', 'Before', 'PrepareDesktopGuard'),
    @('PrepareDesktopGuard', 'deferred', 'Before', 'InstallFiles'),
    @('StopDesktopGuardRollback', 'rollback', 'After', 'InstallFiles'),
    @('ConfigureDesktopGuard', 'deferred', 'After', 'StopDesktopGuardRollback'),
    @('CommitDesktopGuard', 'commit', 'After', 'ConfigureDesktopGuard')
)) {
    $transactionAction = $wix.SelectSingleNode("//wix:CustomAction[@Id='$($entry[0])']", $namespaces)
    $sequence = $wix.SelectSingleNode("//wix:InstallExecuteSequence/wix:Custom[@Action='$($entry[0])']", $namespaces)
    if ($null -eq $transactionAction -or $transactionAction.Execute -ne $entry[1] -or $transactionAction.Impersonate -ne 'no' -or
        $transactionAction.BinaryKey -ne 'MaintenanceRunner' -or $null -eq $sequence -or $sequence.GetAttribute($entry[2]) -ne $entry[3] -or
        $transactionAction.ExeCommand -notmatch '--transaction-id "\[ProductCode\]"' -or $sequence.InnerText -notmatch 'NOT UPGRADINGPRODUCTCODE') {
        throw "Unsafe MSI transaction action or ordering: $($entry[0])"
    }
}
foreach ($id in @('DesktopGuardService', 'DesktopGuardUI', 'DesktopGuardAgent', 'DesktopGuardMaintenance')) {
    $component = $wix.SelectSingleNode("//wix:File[@Id='$id']", $namespaces)
    if ($null -ne $component.Attributes['DefaultVersion']) { throw "Unversioned executable must use the MSI file hash table: $id" }
}

$icon = $wix.SelectSingleNode('//wix:Icon[@Id="DesktopGuardProIcon"]', $namespaces)
if ($null -eq $icon -or $icon.SourceFile -ne '..\assets\desktop-guard-pro.ico') {
    throw 'The product icon must reference the packaged Windows icon asset.'
}

$desktopShortcut = $wix.SelectSingleNode('//wix:Shortcut[@Id="DesktopGuardProShortcut"]', $namespaces)
if ($null -eq $desktopShortcut -or $desktopShortcut.Target -ne '[INSTALLFOLDER]desktop-guard-ui.exe') {
    throw 'The MSI definition must create a desktop shortcut for the UI executable.'
}
$startMenuShortcut = $wix.SelectSingleNode('//wix:Shortcut[@Id="StartMenuDesktopGuardProShortcut"]', $namespaces)
if ($null -eq $startMenuShortcut -or $startMenuShortcut.Target -ne '[INSTALLFOLDER]desktop-guard-ui.exe') {
    throw 'Shortcuts must not reference a file owned by another component.'
}

$desktopDirectory = $wix.SelectSingleNode('//wix:Directory[@Id="CommonDesktopFolder"]', $namespaces)
if ($null -eq $desktopDirectory -or $desktopDirectory.ParentNode.Attributes['Id'].Value -ne 'TARGETDIR') {
    throw 'The desktop shortcut must use CommonDesktopFolder under TARGETDIR.'
}

$programsDirectory = $wix.SelectSingleNode('//wix:Directory[@Id="CommonProgramsFolder"]', $namespaces)
if ($null -eq $programsDirectory -or $programsDirectory.ParentNode.Attributes['Id'].Value -ne 'TARGETDIR') {
    throw 'The Start menu shortcut must use CommonProgramsFolder under TARGETDIR.'
}

$desktopPath = $wix.SelectSingleNode('//wix:CustomAction[@Id="SetCommonDesktopFolder"]', $namespaces)
if ($null -eq $desktopPath -or $desktopPath.Property -ne 'CommonDesktopFolder' -or $desktopPath.Value -ne '[DesktopFolder]') {
    throw 'CommonDesktopFolder must resolve to the Windows public desktop path.'
}

$programsPath = $wix.SelectSingleNode('//wix:CustomAction[@Id="SetCommonProgramsFolder"]', $namespaces)
if ($null -eq $programsPath -or $programsPath.Property -ne 'CommonProgramsFolder' -or $programsPath.Value -ne '[ProgramMenuFolder]') {
    throw 'CommonProgramsFolder must resolve to the Windows public Start menu path.'
}

$uninstallShortcut = $wix.SelectSingleNode('//wix:Shortcut[@Id="StartMenuUninstallShortcut"]', $namespaces)
if ($null -eq $uninstallShortcut -or $uninstallShortcut.Arguments -ne '/x [ProductCode]') {
    throw 'The MSI definition must create a Start menu uninstall shortcut.'
}

$installerUI = $wix.SelectSingleNode('//wix:UI[@Id="DesktopGuardProUI"]', $namespaces)
$installDirectory = $wix.SelectSingleNode('//wix:Property[@Id="WIXUI_INSTALLDIR"]', $namespaces)
$optionsDialog = $wix.SelectSingleNode('//wix:Dialog[@Id="DesktopGuardOptionsDlg"]', $namespaces)
$shortcutOption = $wix.SelectSingleNode('//wix:Dialog[@Id="DesktopGuardOptionsDlg"]/wix:Control[@Id="DesktopShortcutCheckBox"]', $namespaces)
if ($null -eq $installerUI -or $null -eq $optionsDialog -or $null -eq $shortcutOption -or
    $installDirectory.Value -ne 'INSTALLFOLDER' -or $shortcutOption.Type -ne 'CheckBox' -or
    $shortcutOption.Property -ne 'CREATE_DESKTOP_SHORTCUT' -or $shortcutOption.CheckBoxValue -ne '1') {
    throw 'The MSI must provide an install-directory flow with an optional desktop shortcut.'
}

$desktopFeature = $wix.SelectSingleNode('//wix:Feature[@Id="DesktopShortcutFeature"]/wix:ComponentRef[@Id="DesktopShortcut"]', $namespaces)
$addShortcut = $wix.SelectSingleNode('//wix:UI[@Id="DesktopGuardProUI"]/wix:Publish[@Dialog="DesktopGuardOptionsDlg" and @Control="Next" and @Event="AddLocal" and @Value="DesktopShortcutFeature"]', $namespaces)
$removeShortcut = $wix.SelectSingleNode('//wix:UI[@Id="DesktopGuardProUI"]/wix:Publish[@Dialog="DesktopGuardOptionsDlg" and @Control="Next" and @Event="Remove" and @Value="DesktopShortcutFeature"]', $namespaces)
if ($null -eq $desktopFeature -or $null -eq $addShortcut -or $null -eq $removeShortcut -or
    $addShortcut.InnerText -notmatch 'CREATE_DESKTOP_SHORTCUT' -or $removeShortcut.InnerText -notmatch 'CREATE_DESKTOP_SHORTCUT') {
    throw 'The desktop shortcut selection must control its own MSI feature before installation.'
}

$launchText = $wix.SelectSingleNode('//wix:Property[@Id="WIXUI_EXITDIALOGOPTIONALCHECKBOXTEXT"]', $namespaces)
$launchDefault = $wix.SelectSingleNode('//wix:Property[@Id="WIXUI_EXITDIALOGOPTIONALCHECKBOX"]', $namespaces)
$launchAction = $wix.SelectSingleNode('//wix:CustomAction[@Id="LaunchDesktopGuardPro"]', $namespaces)
$launchPublish = $wix.SelectSingleNode('//wix:UI[@Id="DesktopGuardProUI"]/wix:Publish[@Dialog="ExitDialog" and @Control="Finish" and @Event="DoAction" and @Value="LaunchDesktopGuardPro"]', $namespaces)
if ($null -eq $launchText -or $launchText.Value -notmatch 'Desktop Guard Pro' -or $launchDefault.Value -ne '1' -or
    $null -eq $launchAction -or $launchAction.BinaryKey -ne 'WixCA' -or $launchAction.DllEntry -ne 'WixShellExec' -or
    $launchAction.Impersonate -ne 'yes' -or $null -eq $launchPublish -or
    $launchPublish.InnerText -notmatch 'WIXUI_EXITDIALOGOPTIONALCHECKBOX' -or $launchPublish.InnerText -notmatch 'NOT Installed') {
    throw 'The completion dialog must offer an optional non-elevated application launch.'
}

$titleStyle = $wix.SelectSingleNode('//wix:UI[@Id="DesktopGuardProUI"]/wix:TextStyle[@Id="DesktopGuard_Title"]', $namespaces)
if ($null -eq $titleStyle -or $titleStyle.Red -ne '8' -or $titleStyle.Green -ne '43' -or $titleStyle.Blue -ne '94') {
    throw 'The installer title must use the restrained deep-blue product color.'
}

$dialogVariable = $wix.SelectSingleNode('//wix:WixVariable[@Id="WixUIDialogBmp"]', $namespaces)
$bannerVariable = $wix.SelectSingleNode('//wix:WixVariable[@Id="WixUIBannerBmp"]', $namespaces)
$dialogBitmapPath = Join-Path $projectRoot 'assets\installer-dialog.bmp'
$bannerBitmapPath = Join-Path $projectRoot 'assets\installer-banner.bmp'
if ($null -eq $dialogVariable -or $dialogVariable.Value -ne '..\assets\installer-dialog.bmp' -or
    $null -eq $bannerVariable -or $bannerVariable.Value -ne '..\assets\installer-banner.bmp' -or
    -not (Test-Path -LiteralPath $dialogBitmapPath) -or -not (Test-Path -LiteralPath $bannerBitmapPath)) {
    throw 'The installer must replace the default high-saturation WiX artwork with product UI assets.'
}
Add-Type -AssemblyName System.Drawing
$dialogBitmap = [Drawing.Bitmap]::FromFile($dialogBitmapPath)
$bannerBitmap = [Drawing.Bitmap]::FromFile($bannerBitmapPath)
try {
    if ($dialogBitmap.Width -ne 493 -or $dialogBitmap.Height -ne 312 -or
        $dialogBitmap.GetPixel(0, 0).Name -ne 'ff082b5e' -or
        $dialogBitmap.GetPixel(492, 311).Name -ne 'ffffffff') {
        throw 'The installer dialog artwork must use the flat deep-blue and white layout.'
    }
    if ($bannerBitmap.Width -ne 493 -or $bannerBitmap.Height -ne 58 -or
        $bannerBitmap.GetPixel(0, 0).Name -ne 'fff3f7fc' -or
        $bannerBitmap.GetPixel(0, 57).Name -ne 'ffbfd0e6') {
        throw 'The installer banner artwork must use the flat light-blue surface and border.'
    }
} finally {
    $dialogBitmap.Dispose()
    $bannerBitmap.Dispose()
}
