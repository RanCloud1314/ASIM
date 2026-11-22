# Build ASIM module zip. Requires module/bin/mihomo (linux-arm64).
$ErrorActionPreference = "Stop"
$root = Split-Path $Parent:MyInvocation.MyCommand.Path -Parent
if (-not (Test-Path "$root\module\bin\mihomo")) { Write-Error "module/bin/mihomo missing - download mihomo linux-arm64 release first"; exit 1 }
$ver = (Get-Content "$root\module\module.prop" | Where-Object { $_ -match '^version=' }) -replace 'version=',''
$zip = "$root\asim-$ver.zip"
if (Test-Path $zip) { Remove-Item $zip }
Compress-Archive -Path "$root\module\*" -DestinationPath $zip
Write-Host "built $zip"
