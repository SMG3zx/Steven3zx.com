# Builds the host agent and installs it as the Windows service steven3zx-host-agent.
# The service runs `hostagent.exe -supervise`: a supervisor that keeps the agent
# worker running and serves restart requests on 127.0.0.1:9181. The service
# starts at boot and restarts itself if it crashes. Run it once from an elevated
# PowerShell (Run as administrator); re-run it after you change the agent.
#   pwsh portfolio/admin/agent/install.ps1
$ErrorActionPreference = 'Stop'

$serviceName = 'steven3zx-host-agent'
# The per-user scheduled task that earlier versions registered. Removed so two
# copies do not fight over the ports.
$legacyTaskName = 'steven3zx-host-agent'
$agentDir = $PSScriptRoot
$exe = Join-Path $agentDir 'build\hostagent.exe'
$envFile = (Resolve-Path (Join-Path $agentDir '..\..\..\edge\.env')).Path

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$isAdmin = ([Security.Principal.WindowsPrincipal]$identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) { throw 'Run this from an elevated PowerShell (Run as administrator).' }

# Stop the running agent first: Windows will not overwrite a running executable.
if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue
    sc.exe delete $serviceName | Out-Null
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-Service -Name $serviceName -ErrorAction SilentlyContinue) -and (Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 500
    }
}
if (Get-ScheduledTask -TaskName $legacyTaskName -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $legacyTaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $legacyTaskName -Confirm:$false
}
Get-Process hostagent -ErrorAction SilentlyContinue | Stop-Process -Force

Push-Location $agentDir
try {
    go build -trimpath -o $exe .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
}
finally { Pop-Location }

# The service control manager reads BinaryPathName as a command line, so the
# quotes keep each path whole even when it contains spaces.
$binaryPath = '"{0}" -supervise -env "{1}"' -f $exe, $envFile
New-Service -Name $serviceName -BinaryPathName $binaryPath `
    -DisplayName 'Steven3zx host agent' `
    -Description 'Reports on the origin host to the admin map and restarts the agent on request.' `
    -StartupType Automatic | Out-Null

# Restart on crash: after 5s, 5s, then 30s. The counter resets after a day.
sc.exe failure $serviceName reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null

Start-Service -Name $serviceName
Get-Service -Name $serviceName | Format-Table Name, Status, StartType -AutoSize
Write-Host "Installed service '$serviceName' ($exe). Log: $(Join-Path $agentDir 'build\hostagent.log')"
