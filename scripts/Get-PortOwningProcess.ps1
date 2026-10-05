$portNumber = Read-Host "Port Number"
$processes = Get-NetTCPConnection -LocalPort $portNumber
    
foreach ($item in $processes.OwningProcess) {
    Get-Process -PID $item
}