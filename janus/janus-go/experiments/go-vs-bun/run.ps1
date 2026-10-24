# Runs every scenario against the Go and Bun/Elysia gateways and prints a table.
# Usage: pwsh run.ps1 [-Duration 8] [-Conns 64]
param([int]$Duration = 8, [int]$Conns = 64)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$body = '{"name":"steven","age":33,"tags":["a","b","c"]}'
$scenarios = @(
  @{ n = 'ping';     path = '/ping';     body = '' },
  @{ n = 'validate'; path = '/validate'; body = $body },
  @{ n = 'nats';     path = '/nats';     body = '' },
  @{ n = 'cpu';      path = '/cpu';      body = '' }
)
$hub = Start-Process bin\hub.exe -PassThru -WindowStyle Hidden
Start-Sleep 1
$servers = @(
  @{ name = 'go';  port = 8081; exe = 'bin\goserver.exe'; args = @() },
  @{ name = 'bun'; port = 8082; exe = 'bun'; args = @('run', 'bunserver/server.ts') }
)
$rows = @()
try {
  foreach ($s in $servers) {
    $t0 = Get-Date
    $p = Start-Process $s.exe -ArgumentList $s.args -PassThru -WindowStyle Hidden
    do { Start-Sleep -Milliseconds 20; $up = (Test-NetConnection 127.0.0.1 -Port $s.port -WarningAction SilentlyContinue -InformationLevel Quiet) } until ($up)
    $startup = [int]((Get-Date) - $t0).TotalMilliseconds
    $idle = [int]((Get-Process -Id $p.Id).WorkingSet64 / 1MB)
    foreach ($sc in $scenarios) {
      $url = "http://127.0.0.1:$($s.port)$($sc.path)"
      $a = @('-url', $url, '-c', $Conns)
      if ($sc.body) { $a += @('-body', $sc.body) }
      & bin\loadgen.exe @a -d 2s | Out-Null          # warm-up
      $out = & bin\loadgen.exe @a -d "${Duration}s"
      $peak = [int]((Get-Process -Id $p.Id).WorkingSet64 / 1MB)
      $rows += [pscustomobject]@{ server = $s.name; scenario = $sc.n; result = $out; rss_mb = $peak }
    }
    $rows += [pscustomobject]@{ server = $s.name; scenario = 'startup+idle'; result = "startup=${startup}ms"; rss_mb = $idle }
    Stop-Process -Id $p.Id -Force
  }
} finally {
  Stop-Process -Id $hub.Id -Force -ErrorAction SilentlyContinue
}
$rows | Format-Table -AutoSize -Wrap | Out-String -Width 200
