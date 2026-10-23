<# Removes the spotlight startup tasks and stops anything they started. #>
$ErrorActionPreference = 'Stop'
foreach ($name in 'Spotlight Indexer', 'Spotlight UI') {
  if (Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $name -Confirm:$false
    Write-Host "Removed '$name'."
  } else {
    Write-Host "'$name' is not registered."
  }
}
