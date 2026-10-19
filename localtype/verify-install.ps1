$ErrorActionPreference = 'Stop'
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$env:Path = "$env:Path;$userPath"

$ffmpeg = Get-Command ffmpeg.exe -ErrorAction Stop
$whisper = Get-Command whisper-cli.exe -ErrorAction Stop
Write-Host "ffmpeg=$($ffmpeg.Source)"
Write-Host "whisper=$($whisper.Source)"

$model = Join-Path $env:APPDATA 'LocalType\ggml-small.en.bin'
$expected = 'c6138d6d58ecc8322097e0f987c32f1be8bb0a18532a3f88f734d1bbf9c41e5d'
$actual = (Get-FileHash -LiteralPath $model -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'Model hash does not match the published hash.' }
Write-Host "model_sha256=$actual"

$wav = Join-Path $env:TEMP 'localtype-install-check.wav'
$outBase = Join-Path $env:TEMP 'localtype-install-check'
$ffmpegArgs = @('-hide_banner','-loglevel','error','-f','lavfi','-i','anullsrc=r=16000:cl=mono','-t','1','-y',$wav)
& $ffmpeg.Source @ffmpegArgs
if ($LASTEXITCODE -ne 0) { throw "FFmpeg check exited with $LASTEXITCODE." }

$whisperArgs = @('-m',$model,'-f',$wav,'-otxt','-of',$outBase,'-nt')
$stdoutPath = "$outBase.stdout"
$stderrPath = "$outBase.stderr"
$previousErrorPreference = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& $whisper.Source @whisperArgs 1> $stdoutPath 2> $stderrPath
$whisperExit = $LASTEXITCODE
$ErrorActionPreference = $previousErrorPreference
if ($whisperExit -ne 0) { Get-Content $stderrPath; throw "Whisper check exited with $whisperExit." }
$engineLog = (Get-Content $stderrPath) -join "`n"
if ($engineLog -notmatch 'CUDA devices') { throw 'Whisper did not report CUDA initialization.' }
Write-Host (($engineLog -split "`n" | Where-Object { $_ -match 'CUDA devices|Device 0:' }) -join "`n")
Write-Host 'Local model inference completed.'
Remove-Item $wav,"$outBase.txt",$stdoutPath,$stderrPath -Force -ErrorAction SilentlyContinue
