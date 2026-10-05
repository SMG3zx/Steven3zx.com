$listener = Get-NetTCPConnection -State Listen -LocalPort 3000 -ErrorAction SilentlyContinue
if ($listener) {
    Write-Host "SpacetimeDB is already running on 127.0.0.1:3000."
    exit 0
}

$process = Get-Process -Name "spacetimedb-standalone" -ErrorAction SilentlyContinue
if ($process) {
    Write-Host "SpacetimeDB is already starting (PID $($process.Id))."
    exit 0
}

spacetime start --listen-addr 127.0.0.1:3000 --in-memory --non-interactive
exit $LASTEXITCODE
