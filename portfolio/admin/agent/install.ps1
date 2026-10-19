# Builds the host agent and registers a per-user scheduled task that starts it
# hidden at logon (and restarts it if it dies). No elevation needed.
#   pwsh portfolio/admin/agent/install.ps1
$ErrorActionPreference = 'Stop'

$taskName = 'steven3zx-host-agent'
$agentDir = $PSScriptRoot
$exe = Join-Path $agentDir 'build\hostagent.exe'
$envFile = (Resolve-Path (Join-Path $agentDir '..\..\..\edge\.env')).Path

Push-Location $agentDir
try {
    go build -trimpath -o $exe .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
}
finally { Pop-Location }

Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
Get-Process hostagent -ErrorAction SilentlyContinue | Stop-Process -Force

$action = New-ScheduledTaskAction -Execute $exe -Argument "-env `"$envFile`" -addr 127.0.0.1:9180" -WorkingDirectory $agentDir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
    -MultipleInstances IgnoreNew -Hidden
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited

Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $taskName
Write-Host "Registered and started scheduled task '$taskName' ($exe)."
