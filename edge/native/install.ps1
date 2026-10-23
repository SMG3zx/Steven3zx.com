# Installs the native Windows edge: Traefik as the service steven3zx-traefik, the CrowdSec
# engine and its Windows Firewall bouncer, and the Traefik bouncer key. Run it from an
# elevated PowerShell (Run as administrator); it is safe to run again.
#   pwsh edge/native/install.ps1            install everything, leave Docker Traefik serving
#   pwsh edge/native/install.ps1 -Cutover   also switch 80/443 from Docker Traefik to the service
#
# Rollback: Stop-Service steven3zx-traefik; then from edge/:
#   docker compose --profile traefik-docker up -d traefik
#
# Downloads, all from the projects' own GitHub releases:
#   traefik_v3.7.16_windows_amd64.zip   about 52 MB, checked against the release checksums
#   crowdsec_1.8.1.msi                  about 119 MB, signature and SHA-256 shown
#   the Windows Firewall bouncer        from Chocolatey, which also installs the .NET runtime
param(
    [switch]$Cutover,
    # Skip the "continue?" question when a download is not code-signed.
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'

$traefikVersion = '3.7.16'
$crowdsecVersion = '1.8.1'
$lapiPort = 8180   # CrowdSec's default, 8080, is the Plane route's port on this host.

$nativeDir = $PSScriptRoot
$edgeDir = (Resolve-Path (Join-Path $nativeDir '..')).Path
$envFile = Join-Path $edgeDir '.env'
$dataDir = 'C:\steven3zx\edge'
$binDir = Join-Path $dataDir 'bin'
$logDir = Join-Path $dataDir 'logs'
$serviceName = 'steven3zx-traefik'
$crowdsecConfig = 'C:\ProgramData\CrowdSec\config'
$work = Join-Path $env:TEMP 'steven3zx-edge-install'

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not ([Security.Principal.WindowsPrincipal]$identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this from an elevated PowerShell (Run as administrator).'
}
if (-not (Test-Path $envFile)) { throw "Missing $envFile (needs CLOUDFLARE_API_TOKEN and TAILNET_IP)." }

function Get-EnvValue([string]$name) {
    $line = Get-Content $envFile | Where-Object { $_ -match "^$name=" } | Select-Object -First 1
    if (-not $line) { throw "$name is not set in $envFile" }
    $value = ($line -split '=', 2)[1].Trim()
    if ($value.Length -ge 2 -and $value.StartsWith('"') -and $value.EndsWith('"')) { $value = $value.Substring(1, $value.Length - 2) }
    $value
}

# Restricts a secret file to SYSTEM and Administrators.
function Protect-File([string]$path) {
    icacls $path /inheritance:r /grant:r 'SYSTEM:(F)' 'Administrators:(F)' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed on $path" }
}

function Get-Download([string]$url, [string]$name) {
    $target = Join-Path $work $name
    Write-Host "Downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $target -UseBasicParsing
    $target
}

# Shows the SHA-256 and the signature of an installer. An unsigned file stops for a yes.
function Confirm-Installer([string]$path) {
    $hash = (Get-FileHash -Algorithm SHA256 $path).Hash
    $sig = Get-AuthenticodeSignature $path
    Write-Host ("{0}`n  SHA-256   {1}`n  Signature {2} {3}" -f (Split-Path $path -Leaf), $hash, $sig.Status, $sig.SignerCertificate.Subject)
    if ($sig.Status -ne 'Valid' -and -not $Yes) {
        $answer = Read-Host 'Not validly signed. Compare the SHA-256 with the release page, then type yes to continue'
        if ($answer -ne 'yes') { throw 'Stopped at the unsigned installer.' }
    }
}

function Get-CrowdSecDir {
    $service = Get-CimInstance Win32_Service -Filter "Name='crowdsec'" -ErrorAction SilentlyContinue
    if (-not $service) { throw 'The crowdsec service is not installed.' }
    $exe = ($service.PathName -replace '^"([^"]+)".*$', '$1')
    Split-Path $exe
}

Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $work, $binDir, $logDir | Out-Null

# --- Traefik and the service wrapper -------------------------------------------------
$zip = Get-Download "https://github.com/traefik/traefik/releases/download/v$traefikVersion/traefik_v${traefikVersion}_windows_amd64.zip" 'traefik.zip'
$sums = Get-Download "https://github.com/traefik/traefik/releases/download/v$traefikVersion/traefik_v${traefikVersion}_checksums.txt" 'traefik-checksums.txt'
$expected = (Get-Content $sums | Where-Object { $_ -match "traefik_v${traefikVersion}_windows_amd64\.zip" } | Select-Object -First 1) -split '\s+' | Select-Object -First 1
if (-not $expected) { throw 'The checksums file has no entry for the Windows zip.' }
$actual = (Get-FileHash -Algorithm SHA256 $zip).Hash
if ($actual -ne $expected.ToUpper()) { throw "Traefik zip checksum mismatch: got $actual, expected $expected" }
Write-Host "Traefik zip checksum ok ($actual)"

if (Get-Service $serviceName -ErrorAction SilentlyContinue) {
    Stop-Service $serviceName -Force -ErrorAction SilentlyContinue
    sc.exe delete $serviceName | Out-Null
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-Service $serviceName -ErrorAction SilentlyContinue) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
}
Get-Process traefik, svcwrap -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$binDir*" } | Stop-Process -Force

Expand-Archive -Path $zip -DestinationPath (Join-Path $work 'traefik') -Force
Copy-Item (Join-Path $work 'traefik\traefik.exe') (Join-Path $binDir 'traefik.exe') -Force

Push-Location (Join-Path $edgeDir 'svcwrap')
try {
    go build -trimpath -o (Join-Path $binDir 'svcwrap.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'go build of svcwrap failed' }
}
finally { Pop-Location }

# --- Certificates and secrets ---------------------------------------------------------
# Reuse the certificates Docker Traefik already holds, so the wildcard is not re-issued.
$acme = Join-Path $dataDir 'acme.json'
if (-not (Test-Path $acme)) {
    docker run --rm -v steven3zx-edge_traefik_data:/l:ro -v "${dataDir}:/out" alpine cp /l/acme.json /out/acme.json
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path $acme)) { throw 'Could not copy acme.json out of the traefik_data volume.' }
}
Protect-File $acme

$traefikEnv = Join-Path $dataDir 'traefik.env'
Set-Content -Path $traefikEnv -Value ("CF_DNS_API_TOKEN=" + (Get-EnvValue 'CLOUDFLARE_API_TOKEN')) -Encoding ascii
Protect-File $traefikEnv

# --- Static config ---------------------------------------------------------------------
$tailnetIp = Get-EnvValue 'TAILNET_IP'
$config = Join-Path $dataDir 'traefik.yml'
& (Join-Path $nativeDir 'render-config.ps1') -Out $config -DataDir $dataDir -PrivateAddr "${tailnetIp}:8443"

# A fresh access log each install, since Traefik cannot reopen one on Windows.
$accessLog = Join-Path $logDir 'traefik-access.log'
if (Test-Path $accessLog) { Move-Item $accessLog "$accessLog.$(Get-Date -Format yyyyMMdd-HHmmss)" -Force }
New-Item -ItemType File -Path $accessLog -Force | Out-Null

# --- CrowdSec engine ---------------------------------------------------------------------
if (-not (Get-Service crowdsec -ErrorAction SilentlyContinue)) {
    $msi = Get-Download "https://github.com/crowdsecurity/crowdsec/releases/download/v$crowdsecVersion/crowdsec_$crowdsecVersion.msi" 'crowdsec.msi'
    Confirm-Installer $msi
    Start-Process msiexec.exe -ArgumentList '/i', "`"$msi`"", '/qn', '/norestart' -Wait
    if (-not (Get-Service crowdsec -ErrorAction SilentlyContinue)) { throw 'The CrowdSec installer did not create the crowdsec service.' }
}
$csDir = Get-CrowdSecDir
$cscli = Join-Path $csDir 'cscli.exe'
if (-not (Test-Path $cscli)) { throw "cscli.exe not found beside the crowdsec service in $csDir" }

# Move the local API off 8080, and point the engine's own credentials at the new port.
Stop-Service crowdsec -ErrorAction SilentlyContinue
$mainConfig = Join-Path $crowdsecConfig 'config.yaml'
$credentials = Join-Path $crowdsecConfig 'local_api_credentials.yaml'
(Get-Content -Raw $mainConfig) -replace '(listen_uri:\s*)\S+', "`${1}127.0.0.1:$lapiPort" | Set-Content $mainConfig -Encoding utf8
(Get-Content -Raw $credentials) -replace '(url:\s*https?://[^:/\s]+):\d+', "`${1}:$lapiPort" | Set-Content $credentials -Encoding utf8

$parserDir = Join-Path $crowdsecConfig 'parsers\s02-enrich'
New-Item -ItemType Directory -Force -Path $parserDir | Out-Null
Copy-Item (Join-Path $nativeDir 'crowdsec\whitelist.yaml') (Join-Path $parserDir 'steven3zx-whitelist.yaml') -Force

$acquis = Join-Path $crowdsecConfig 'acquis.yaml'
if (-not (Select-String -Path $acquis -SimpleMatch 'traefik-access.log' -Quiet)) {
    Add-Content -Path $acquis -Value ("`n" + (Get-Content -Raw (Join-Path $nativeDir 'crowdsec\acquis-traefik.yaml')))
}

& $cscli hub update
& $cscli collections install crowdsecurity/traefik crowdsecurity/base-http-scenarios crowdsecurity/http-cve
Start-Service crowdsec
Start-Sleep -Seconds 5

# --- Bouncers -------------------------------------------------------------------------------
function New-BouncerKey([string]$name) {
    & $cscli bouncers delete $name 2>$null | Out-Null
    $key = (& $cscli bouncers add $name -o raw).Trim()
    if (-not $key) { throw "cscli returned no key for bouncer $name" }
    $key
}

$traefikKey = Join-Path $dataDir 'crowdsec-traefik.key'
Set-Content -Path $traefikKey -Value (New-BouncerKey 'traefik') -NoNewline -Encoding ascii
Protect-File $traefikKey

$fwServiceName = 'cs-windows-firewall-bouncer'
if (-not (Get-Service $fwServiceName -ErrorAction SilentlyContinue)) {
    choco install crowdsec-windows-firewall-bouncer -y
    if (-not (Get-Service $fwServiceName -ErrorAction SilentlyContinue)) { throw "The Chocolatey package did not create the $fwServiceName service." }
}
# The package installs its config under CrowdSec's config folder, with the default local API
# port (8080). Point it at the moved port and a key of its own.
$fwConfig = Join-Path $crowdsecConfig 'bouncers\cs-windows-firewall-bouncer.yaml'
if (-not (Test-Path $fwConfig)) { throw "Firewall bouncer config not found at $fwConfig" }
$fwKey = New-BouncerKey 'windows-firewall'
(Get-Content -Raw $fwConfig) `
    -replace '(?m)^([ \t]*api_endpoint:[ \t]*).*$', "`${1}http://127.0.0.1:$lapiPort" `
    -replace '(?m)^([ \t]*api_key:[ \t]*).*$', ('${1}' + $fwKey.Replace('$', '$$')) | Set-Content $fwConfig -Encoding utf8
Protect-File $fwConfig
Restart-Service $fwServiceName

# --- Windows Firewall rules for Traefik -------------------------------------------------------
$traefikExe = Join-Path $binDir 'traefik.exe'
Get-NetFirewallRule -DisplayName 'steven3zx Traefik*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName 'steven3zx Traefik web' -Direction Inbound -Action Allow -Program $traefikExe `
    -Protocol TCP -LocalPort 80, 443 | Out-Null
New-NetFirewallRule -DisplayName 'steven3zx Traefik web (HTTP/3)' -Direction Inbound -Action Allow -Program $traefikExe `
    -Protocol UDP -LocalPort 443 | Out-Null
New-NetFirewallRule -DisplayName 'steven3zx Traefik private' -Direction Inbound -Action Allow -Program $traefikExe `
    -Protocol TCP -LocalPort 8443 -RemoteAddress 100.64.0.0/10, fd7a:115c:a1e0::/48 | Out-Null

# --- The service ---------------------------------------------------------------------------------
$binaryPath = '"{0}" -name {1} -dir "{2}" -env "{3}" -log "{4}" -- "{5}" --configFile="{6}"' -f `
    (Join-Path $binDir 'svcwrap.exe'), $serviceName, $dataDir, $traefikEnv, (Join-Path $logDir 'traefik.log'), $traefikExe, $config
New-Service -Name $serviceName -BinaryPathName $binaryPath -DisplayName 'Steven3zx Traefik' `
    -Description 'Native Traefik edge proxy for steven3zx.com, with the CrowdSec bouncer.' -StartupType Automatic | Out-Null
sc.exe failure $serviceName reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null

# Backend containers publish loopback ports for Traefik; recreate them so the ports exist.
Push-Location $edgeDir
try {
    docker compose up -d portfolio admin gitea headscale
    if ($LASTEXITCODE -ne 0) { throw 'docker compose up failed' }
}
finally { Pop-Location }

if ($Cutover) {
    docker stop steven3zx-edge-traefik-1
    Start-Service $serviceName
    Start-Sleep -Seconds 5
    Get-Service $serviceName | Format-Table Name, Status, StartType -AutoSize
    Write-Host "Cut over. Rollback: Stop-Service $serviceName; docker compose --profile traefik-docker up -d traefik (from edge/)."
}
else {
    Write-Host "Installed service '$serviceName' (stopped). Run again with -Cutover to switch 80/443 to it."
}
