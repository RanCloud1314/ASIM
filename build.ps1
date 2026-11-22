# Build the ASIM KernelSU module zip.
$ErrorActionPreference = "Stop"
$root = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path }
$ver = (Get-Content "$root\module\module.prop" | Where-Object { $_ -match '^version=' }) -replace 'version=',''
$zip = "$root\asim-$ver.zip"
if (Test-Path $zip) { Remove-Item $zip }
Compress-Archive -Path "$root\module\*" -DestinationPath $zip
Write-Host "built $zip"
