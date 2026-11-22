# Build the ASIM KernelSU module zip and the relay binaries.
$ErrorActionPreference = "Stop"
$root = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path }
$ver = (Get-Content "$root\module\module.prop" | Where-Object { $_ -match '^version=' }) -replace 'version=',''
$zip = "$root\asim-$ver.zip"

# relay: linux amd64 for the router, windows amd64 for a PC that runs the relay.
$env:CGO_ENABLED = "0"
$relay = "$root\router\asim-relay"
Push-Location $relay
try {
  $env:GOOS = "linux"; $env:GOARCH = "amd64"
  go build -trimpath -ldflags "-s -w" -o asim-relay-linux-amd64 .
  $env:GOOS = "windows"
  go build -trimpath -ldflags "-s -w" -o asim-relay-windows-amd64.exe .
} finally {
  Pop-Location
}

if (Test-Path $zip) { Remove-Item $zip }
Compress-Archive -Path "$root\module\*" -DestinationPath $zip
Write-Host "built $zip"
Write-Host "built $relay\asim-relay-linux-amd64"
Write-Host "built $relay\asim-relay-windows-amd64.exe"
