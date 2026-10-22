<#
.SYNOPSIS
  Start the spotlight indexer and launcher automatically when you log in.

.DESCRIPTION
  Registers two per-user scheduled tasks (no administrator rights needed):
    "Spotlight Indexer"  runs bin\indexd.exe 30 seconds after logon, with no console window,
                         logging to %LOCALAPPDATA%\spotlight\indexd.log
    "Spotlight UI"       runs ui\build\bin\spotlight-ui.exe at logon (the window stays hidden
                         until you press the hotkey)
  Both restart themselves if they crash and are not killed after 72 hours (the Task
  Scheduler default). Re-run this script any time to update them.

.PARAMETER Build
  Build bin\indexd.exe (windowless) and the UI first. Needs Go and the Wails CLI.

.EXAMPLE
  .\scripts\install-startup.ps1 -Build
#>
[CmdletBinding()]
param(
  [string]$Config = (Join-Path (Split-Path $PSScriptRoot -Parent) 'config.json'),
  [string]$LogFile = (Join-Path $env:LOCALAPPDATA 'spotlight\indexd.log'),
  [int]$IndexerDelaySeconds = 30,
  [switch]$Build,
  [switch]$SkipUi
)
$ErrorActionPreference = 'Stop'

$root    = Split-Path $PSScriptRoot -Parent
$indexd  = Join-Path $root 'bin\indexd.exe'
$ui      = Join-Path $root 'ui\build\bin\spotlight-ui.exe'
$user    = "$env:USERDOMAIN\$env:USERNAME"

if ($Build) {
  Write-Host 'Building indexd (windowless)...'
  Push-Location $root
  try {
    go build -ldflags '-H=windowsgui -s -w' -o $indexd ./cmd/indexd
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
  } finally { Pop-Location }
  if (-not $SkipUi) {
    Write-Host 'Building the launcher...'
    Push-Location (Join-Path $root 'ui')
    try {
      wails build
      if ($LASTEXITCODE -ne 0) { throw 'wails build failed' }
    } finally { Pop-Location }
  }
}

if (-not (Test-Path $Config)) { throw "Config not found: $Config (copy config.example.json to config.json)" }
if (-not (Test-Path $indexd)) { throw "Missing $indexd. Run again with -Build." }
if (-not $SkipUi -and -not (Test-Path $ui)) { throw "Missing $ui. Run again with -Build, or use -SkipUi." }

$principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
$settings  = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -ExecutionTimeLimit ([TimeSpan]::Zero) `
  -MultipleInstances IgnoreNew `
  -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) `
  -StartWhenAvailable

function Register-Spotlight($name, $exe, $arguments, $workDir, $delaySeconds) {
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
  if ($delaySeconds -gt 0) { $trigger.Delay = "PT${delaySeconds}S" }
  $action = New-ScheduledTaskAction -Execute $exe -Argument $arguments -WorkingDirectory $workDir
  Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger `
    -Principal $principal -Settings $settings -Force | Out-Null
  Write-Host "Registered '$name'."
}

$indexdArgs = "-config `"$Config`" -log `"$LogFile`""
Register-Spotlight 'Spotlight Indexer' $indexd $indexdArgs $root $IndexerDelaySeconds
if (-not $SkipUi) {
  Register-Spotlight 'Spotlight UI' $ui '' (Split-Path $ui -Parent) 0
}

Write-Host ''
Write-Host "Done. They start at your next logon. To start them now:"
Write-Host "  Start-ScheduledTask 'Spotlight Indexer'; Start-ScheduledTask 'Spotlight UI'"
Write-Host "Log file: $LogFile"
