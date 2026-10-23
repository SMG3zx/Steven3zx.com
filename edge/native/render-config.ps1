# Renders traefik/traefik.template.yml into a static config for the native Traefik.
# install.ps1 calls it for the real ports; run it by hand with other ports to test beside
# the Docker Traefik:
#   pwsh edge/native/render-config.ps1 -Out C:\steven3zx\edge\test\traefik.yml `
#       -DataDir C:\steven3zx\edge\test -WebAddr ':18080' -SecureAddr ':18443' `
#       -PrivateAddr '127.0.0.1:18444' -TrustLoopback
param(
    [Parameter(Mandatory)] [string]$Out,
    [Parameter(Mandatory)] [string]$DataDir,
    [string]$PrivateAddr = '127.0.0.1:8443',
    [string]$WebAddr = ':80',
    [string]$SecureAddr = ':443',
    [string]$DynamicDir = (Join-Path $PSScriptRoot 'traefik\dynamic'),
    # Test only: also trust forwarded headers from loopback, to exercise the real-address path.
    [switch]$TrustLoopback
)
$ErrorActionPreference = 'Stop'

# Traefik takes forward slashes on Windows, and a backslash would be read as a YAML escape.
function ConvertTo-Slashes([string]$path) { $path.Replace('\', '/') }

$text = Get-Content -Raw (Join-Path $PSScriptRoot 'traefik\traefik.template.yml')
$extra = if ($TrustLoopback) { '- "127.0.0.1/32"' } else { '' }

$values = @{
    'WEB_ADDR'      = $WebAddr
    'SECURE_ADDR'   = $SecureAddr
    'PRIVATE_ADDR'  = $PrivateAddr
    'DATA_DIR'      = ConvertTo-Slashes $DataDir
    'DYNAMIC_DIR'   = ConvertTo-Slashes $DynamicDir
    'EXTRA_TRUSTED' = $extra
}
foreach ($key in $values.Keys) {
    $text = $text.Replace("{{$key}}", $values[$key])
}
if ($text -match '\{\{[A-Z_]+\}\}') { throw "Unfilled placeholder in the template: $($Matches[0])" }

New-Item -ItemType Directory -Force -Path (Split-Path $Out) | Out-Null
Set-Content -Path $Out -Value $text -Encoding utf8
Write-Host "Wrote $Out"
