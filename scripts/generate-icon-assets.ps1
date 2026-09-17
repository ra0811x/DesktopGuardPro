[CmdletBinding()]
param(
    [string]$IconPath,
    [string]$BannerPath,
    [string]$DialogPath,
    [switch]$InstallerOnly
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
if ([string]::IsNullOrWhiteSpace($IconPath)) {
    $IconPath = Join-Path (Split-Path -Parent $PSScriptRoot) 'assets\desktop-guard-pro.ico'
}
if ([string]::IsNullOrWhiteSpace($BannerPath)) {
    $BannerPath = Join-Path (Split-Path -Parent $PSScriptRoot) 'assets\installer-banner.bmp'
}
if ([string]::IsNullOrWhiteSpace($DialogPath)) {
    $DialogPath = Join-Path (Split-Path -Parent $PSScriptRoot) 'assets\installer-dialog.bmp'
}

function Get-IconMasterBitmap([string]$Path) {
    $encoded = [IO.File]::ReadAllBytes((Resolve-Path -LiteralPath $Path).Path)
    if ($encoded.Length -lt 22 -or [BitConverter]::ToUInt16($encoded, 2) -ne 1) {
        throw 'The source icon has an invalid ICO header.'
    }
    $count = [BitConverter]::ToUInt16($encoded, 4)
    $selected = $null
    for ($index = 0; $index -lt $count; $index++) {
        $entryOffset = 6 + 16 * $index
        $width = [int]$encoded[$entryOffset]
        if ($width -eq 0) { $width = 256 }
        if ($null -eq $selected -or $width -gt $selected.Width) {
            $selected = [pscustomobject]@{
                Width = $width
                Size = [BitConverter]::ToUInt32($encoded, $entryOffset + 8)
                Offset = [BitConverter]::ToUInt32($encoded, $entryOffset + 12)
            }
        }
    }
    if ($null -eq $selected -or $selected.Offset + $selected.Size -gt $encoded.Length) {
        throw 'The source icon does not contain a usable master layer.'
    }
    $layer = New-Object byte[] $selected.Size
    [Array]::Copy($encoded, $selected.Offset, $layer, 0, $selected.Size)
    $stream = [IO.MemoryStream]::new($layer)
    try {
        $decoded = [Drawing.Bitmap]::new($stream)
        try {
            return [Drawing.Bitmap]$decoded.Clone()
        }
        finally {
            $decoded.Dispose()
        }
    }
    finally {
        $stream.Dispose()
    }
}

function Get-VisibleBounds([Drawing.Bitmap]$Bitmap) {
    $minimumX = $Bitmap.Width
    $minimumY = $Bitmap.Height
    $maximumX = -1
    $maximumY = -1
    for ($y = 0; $y -lt $Bitmap.Height; $y++) {
        for ($x = 0; $x -lt $Bitmap.Width; $x++) {
            if ($Bitmap.GetPixel($x, $y).A -le 8) { continue }
            if ($x -lt $minimumX) { $minimumX = $x }
            if ($x -gt $maximumX) { $maximumX = $x }
            if ($y -lt $minimumY) { $minimumY = $y }
            if ($y -gt $maximumY) { $maximumY = $y }
        }
    }
    if ($maximumX -lt 0) { throw 'The source icon master layer is empty.' }
    return [Drawing.Rectangle]::FromLTRB($minimumX, $minimumY, $maximumX + 1, $maximumY + 1)
}

function New-PaddedIconLayer([Drawing.Bitmap]$Master, [Drawing.Rectangle]$SourceBounds, [int]$Size) {
    $padding = [Math]::Max(2, [Math]::Ceiling($Size * 0.08))
    $available = $Size - 2 * $padding
    $scale = [Math]::Min($available / $SourceBounds.Width, $available / $SourceBounds.Height)
    $targetWidth = [Math]::Max(1, [int][Math]::Round($SourceBounds.Width * $scale))
    $targetHeight = [Math]::Max(1, [int][Math]::Round($SourceBounds.Height * $scale))
    $targetX = [int][Math]::Floor(($Size - $targetWidth) / 2)
    $targetY = [int][Math]::Floor(($Size - $targetHeight) / 2)
    $bitmap = [Drawing.Bitmap]::new($Size, $Size, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $graphics.Clear([Drawing.Color]::Transparent)
        $graphics.CompositingMode = [Drawing.Drawing2D.CompositingMode]::SourceCopy
        $graphics.CompositingQuality = [Drawing.Drawing2D.CompositingQuality]::HighQuality
        $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::HighQuality
        $graphics.DrawImage(
            $Master,
            [Drawing.Rectangle]::new($targetX, $targetY, $targetWidth, $targetHeight),
            $SourceBounds,
            [Drawing.GraphicsUnit]::Pixel)
    }
    finally {
        $graphics.Dispose()
    }
    return $bitmap
}

function Convert-BitmapToPngBytes([Drawing.Bitmap]$Bitmap) {
    $stream = [IO.MemoryStream]::new()
    try {
        $Bitmap.Save($stream, [Drawing.Imaging.ImageFormat]::Png)
        return $stream.ToArray()
    }
    finally {
        $stream.Dispose()
    }
}

function Write-PngIcon([string]$Path, [object[]]$Layers) {
    $stream = [IO.MemoryStream]::new()
    $writer = [IO.BinaryWriter]::new($stream)
    try {
        $writer.Write([uint16]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]$Layers.Count)
        $offset = 6 + 16 * $Layers.Count
        foreach ($layer in $Layers) {
            $dimension = if ($layer.Size -eq 256) { 0 } else { $layer.Size }
            $writer.Write([byte]$dimension)
            $writer.Write([byte]$dimension)
            $writer.Write([byte]0)
            $writer.Write([byte]0)
            $writer.Write([uint16]1)
            $writer.Write([uint16]32)
            $writer.Write([uint32]$layer.Bytes.Length)
            $writer.Write([uint32]$offset)
            $offset += $layer.Bytes.Length
        }
        foreach ($layer in $Layers) { $writer.Write([byte[]]$layer.Bytes) }
        $writer.Flush()
        [IO.File]::WriteAllBytes($Path, $stream.ToArray())
    }
    finally {
        $writer.Dispose()
        $stream.Dispose()
    }
}

function Write-InstallerBitmaps(
    [Drawing.Bitmap]$Master,
    [Drawing.Rectangle]$SourceBounds,
    [string]$BannerOutputPath,
    [string]$DialogOutputPath) {
    $banner = [Drawing.Bitmap]::new(493, 58, [Drawing.Imaging.PixelFormat]::Format24bppRgb)
    $bannerIcon = New-PaddedIconLayer $Master $SourceBounds 48
    $bannerGraphics = [Drawing.Graphics]::FromImage($banner)
    try {
        $bannerGraphics.Clear([Drawing.ColorTranslator]::FromHtml('#F3F7FC'))
        $bannerGraphics.DrawImageUnscaled($bannerIcon, 437, 5)
        $borderPen = [Drawing.Pen]::new([Drawing.ColorTranslator]::FromHtml('#BFD0E6'))
        try {
            $bannerGraphics.DrawLine($borderPen, 0, 57, 492, 57)
        }
        finally {
            $borderPen.Dispose()
        }
        $banner.Save($BannerOutputPath, [Drawing.Imaging.ImageFormat]::Bmp)
    }
    finally {
        $bannerGraphics.Dispose()
        $bannerIcon.Dispose()
        $banner.Dispose()
    }

    $dialog = [Drawing.Bitmap]::new(493, 312, [Drawing.Imaging.PixelFormat]::Format24bppRgb)
    $dialogIcon = New-PaddedIconLayer $Master $SourceBounds 112
    $dialogGraphics = [Drawing.Graphics]::FromImage($dialog)
    try {
        $dialogGraphics.Clear([Drawing.Color]::White)
        $panelBrush = [Drawing.SolidBrush]::new([Drawing.ColorTranslator]::FromHtml('#082B5E'))
        try {
            $dialogGraphics.FillRectangle($panelBrush, 0, 0, 164, 312)
        }
        finally {
            $panelBrush.Dispose()
        }
        $dialogGraphics.DrawImageUnscaled($dialogIcon, 26, 41)
        $dialog.Save($DialogOutputPath, [Drawing.Imaging.ImageFormat]::Bmp)
    }
    finally {
        $dialogGraphics.Dispose()
        $dialogIcon.Dispose()
        $dialog.Dispose()
    }
}

$master = Get-IconMasterBitmap $IconPath
try {
    $sourceBounds = Get-VisibleBounds $master
    if (-not $InstallerOnly) {
        $layers = @()
        foreach ($size in @(16, 24, 32, 48, 64, 128, 256)) {
            $bitmap = New-PaddedIconLayer $master $sourceBounds $size
            try {
                $layers += [pscustomobject]@{ Size = $size; Bytes = Convert-BitmapToPngBytes $bitmap }
            }
            finally {
                $bitmap.Dispose()
            }
        }
        Write-PngIcon $IconPath $layers
    }
    Write-InstallerBitmaps $master $sourceBounds $BannerPath $DialogPath
}
finally {
    $master.Dispose()
}

Write-Output $IconPath
Write-Output $BannerPath
Write-Output $DialogPath
