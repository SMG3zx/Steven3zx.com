# Refreshes derper's copy of the wildcard certificate from Traefik's storage and
# restarts the relay when the certificate changed. Traefik renews its own copy in
# acme.json inside the steven3zx-edge_traefik_data volume; derper reads files from
# C:\janus\derp\certs, so this copies the renewed pair across.
#
# Needs Docker Desktop running. It never prints the key, and it removes the temporary
# copy of acme.json (which holds private keys) when it finishes.

$ErrorActionPreference = 'Stop'
$volume  = 'steven3zx-edge_traefik_data'
$certDir = 'C:\janus\derp\certs'
$name    = 'derp.steven3zx.com'
$task    = 'Janus derper'
$tmp     = Join-Path $env:TEMP ("acme-" + [guid]::NewGuid().ToString('N') + '.json')

$cid = (docker create -v "${volume}:/src:ro" alpine 2>$null)
if (-not $cid) { throw 'could not create a helper container; is Docker running?' }
try {
    docker cp "${cid}:/src/acme.json" $tmp 2>&1 | Out-Null
    if (-not (Test-Path $tmp)) { throw 'acme.json was not found in the Traefik volume' }
    $acme  = Get-Content $tmp -Raw | ConvertFrom-Json
    $certs = foreach ($resolver in $acme.PSObject.Properties) { $resolver.Value.Certificates }
    $wild  = $certs | Where-Object { $_.domain.main -eq '*.steven3zx.com' } | Select-Object -First 1
    if (-not $wild) { throw 'no wildcard certificate for *.steven3zx.com in acme.json' }

    $crt = [Convert]::FromBase64String($wild.certificate)
    $key = [Convert]::FromBase64String($wild.key)
    $crtPath = Join-Path $certDir "$name.crt"
    $keyPath = Join-Path $certDir "$name.key"

    $newHash = (Get-FileHash -InputStream ([IO.MemoryStream]::new($crt)) -Algorithm SHA256).Hash
    $oldHash = if (Test-Path $crtPath) { (Get-FileHash $crtPath -Algorithm SHA256).Hash } else { '' }
    if ($newHash -eq $oldHash) { 'certificate unchanged'; return }

    [IO.File]::WriteAllBytes($crtPath, $crt)
    [IO.File]::WriteAllBytes($keyPath, $key)
    icacls $keyPath /inheritance:r /grant:r "$($env:USERNAME):(R)" | Out-Null
    'certificate updated; restarting derper'

    Get-CimInstance Win32_Process -Filter "Name='derper.exe'" |
        Where-Object { $_.ExecutablePath -eq 'C:\janus\derp\bin\derper.exe' } |
        ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
    Start-Sleep -Seconds 2
    Start-ScheduledTask -TaskName $task
} finally {
    docker rm -f $cid 2>$null | Out-Null
    Remove-Item $tmp -Force -ErrorAction SilentlyContinue
}
