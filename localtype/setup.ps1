$ErrorActionPreference = 'Stop'

$root = Join-Path $env:LOCALAPPDATA 'Programs\LocalType\tools'
$ffmpegInstall = Join-Path $root 'ffmpeg'
$whisperInstall = Join-Path $root 'whisper-cuda'
$appData = Join-Path $env:APPDATA 'LocalType'
$staging = Join-Path $env:TEMP 'LocalType-setup'
$ffmpegUrl = 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip'
$whisperUrl = 'https://github.com/ggml-org/whisper.cpp/releases/download/v1.8.3/whisper-cublas-12.4.0-bin-x64.zip'
$ffmpegShaUrl = "$ffmpegUrl.sha256"
$whisperSha256 = 'c12a563333d3c3707be70754dc0e87c1cb58aa6333a87055bbcf9b524488dfb0'
$modelUrl = 'https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin'
$modelSha256 = 'c6138d6d58ecc8322097e0f987c32f1be8bb0a18532a3f88f734d1bbf9c41e5d'

New-Item -ItemType Directory -Force -Path $root, $staging, $appData | Out-Null
$ffmpegArchive = Join-Path $staging 'ffmpeg-release-essentials.zip'
$whisperArchive = Join-Path $staging 'whisper-cuda.zip'
$modelPath = Join-Path $appData 'ggml-small.en.bin'

function Get-VerifiedDownload([string] $Uri, [string] $Path, [string] $ExpectedSha256) {
    if (-not (Test-Path -LiteralPath $Path)) {
        Write-Host "Downloading $([IO.Path]::GetFileName($Path)) ..."
        & curl.exe --fail --location --silent --show-error --output $Path $Uri
        if ($LASTEXITCODE -ne 0) { Remove-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue; throw "curl failed downloading $Uri (exit $LASTEXITCODE)." }
    }
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $ExpectedSha256.ToLowerInvariant()) {
        Remove-Item -LiteralPath $Path -Force
        throw "SHA-256 verification failed for $Path. Expected $ExpectedSha256, got $actual."
    }
}

$ffmpegExe = Get-ChildItem -LiteralPath $ffmpegInstall -Filter ffmpeg.exe -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $ffmpegExe) {
    Write-Host 'Fetching the current FFmpeg archive checksum...'
    $ffmpegShaFile = Join-Path $staging 'ffmpeg.sha256'
    & curl.exe --fail --location --silent --show-error --output $ffmpegShaFile $ffmpegShaUrl
    if ($LASTEXITCODE -ne 0) { throw "curl failed fetching the FFmpeg checksum (exit $LASTEXITCODE)." }
    $ffmpegShaText = (Get-Content -LiteralPath $ffmpegShaFile -Raw).Trim()
    $ffmpegSha256 = ($ffmpegShaText -split '\s+')[0].ToLowerInvariant()
    if ($ffmpegSha256 -notmatch '^[0-9a-f]{64}$') { throw 'Could not read the FFmpeg SHA-256 from the publisher.' }
    Get-VerifiedDownload $ffmpegUrl $ffmpegArchive $ffmpegSha256
    Write-Host 'Extracting FFmpeg...'
    Expand-Archive -LiteralPath $ffmpegArchive -DestinationPath $ffmpegInstall -Force
    $ffmpegExe = Get-ChildItem -LiteralPath $ffmpegInstall -Filter ffmpeg.exe -Recurse | Select-Object -First 1
}
if (-not $ffmpegExe) { throw 'FFmpeg archive did not contain ffmpeg.exe.' }

$whisperExe = Get-ChildItem -LiteralPath $whisperInstall -Filter whisper-cli.exe -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $whisperExe) {
    Get-VerifiedDownload $whisperUrl $whisperArchive $whisperSha256
    Write-Host 'Extracting Whisper.cpp CUDA build...'
    Expand-Archive -LiteralPath $whisperArchive -DestinationPath $whisperInstall -Force
    $whisperExe = Get-ChildItem -LiteralPath $whisperInstall -Filter whisper-cli.exe -Recurse | Select-Object -First 1
}
if (-not $whisperExe) { throw 'Whisper.cpp archive did not contain whisper-cli.exe.' }

if (-not (Test-Path -LiteralPath $modelPath)) {
    $partialModel = "$modelPath.download"
    Write-Host 'Downloading the English small.en model (about 465 MiB)...'
    & curl.exe --fail --location --silent --show-error --output $partialModel $modelUrl
    if ($LASTEXITCODE -ne 0) { Remove-Item -LiteralPath $partialModel -Force -ErrorAction SilentlyContinue; throw "curl failed downloading the model (exit $LASTEXITCODE)." }
    $actualModelHash = (Get-FileHash -LiteralPath $partialModel -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualModelHash -ne $modelSha256) {
        Remove-Item -LiteralPath $partialModel -Force
        throw "Whisper model SHA-256 verification failed. Expected $modelSha256, got $actualModelHash."
    }
    Move-Item -LiteralPath $partialModel -Destination $modelPath -Force
}
$actualModelHash = (Get-FileHash -LiteralPath $modelPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualModelHash -ne $modelSha256) { throw "Installed Whisper model SHA-256 verification failed. Expected $modelSha256, got $actualModelHash." }

$binPaths = @($ffmpegExe.Directory.FullName, $whisperExe.Directory.FullName)
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathParts = @($userPath -split ';' | Where-Object { $_ })
foreach ($binPath in $binPaths) {
    if (-not ($pathParts | Where-Object { $_.TrimEnd('\') -ieq $binPath.TrimEnd('\') })) {
        $pathParts += $binPath
    }
}
[Environment]::SetEnvironmentVariable('Path', ($pathParts -join ';'), 'User')
foreach ($binPath in $binPaths) {
    if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $binPath.TrimEnd('\') })) {
        $env:Path = "$env:Path;$binPath"
    }
}
Add-Type -Namespace LocalType -Name PathBroadcast -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr hWnd, uint Msg, System.IntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out System.IntPtr lpdwResult);
'@ -ErrorAction SilentlyContinue
$broadcastResult = [IntPtr]::Zero
[LocalType.PathBroadcast]::SendMessageTimeout([IntPtr]::new(-1), 0x001A, [IntPtr]::Zero, 'Environment', 0x0002, 1000, [ref]$broadcastResult) | Out-Null

Write-Host "FFmpeg: $(& (Join-Path $ffmpegExe.Directory.FullName 'ffmpeg.exe') -version | Select-Object -First 1)"
$whisperOut = Join-Path $staging 'whisper-version.out'
$whisperErr = Join-Path $staging 'whisper-version.err'
$whisperVersion = Start-Process -FilePath $whisperExe.FullName -ArgumentList '--version' -NoNewWindow -Wait -PassThru -RedirectStandardOutput $whisperOut -RedirectStandardError $whisperErr
if ($whisperVersion.ExitCode -ne 0) { throw "whisper-cli --version exited with code $($whisperVersion.ExitCode). See $whisperErr" }
Write-Host "Whisper CLI: $($whisperExe.FullName)"
Write-Host "Model: $modelPath"
Remove-Item -LiteralPath $ffmpegArchive, $whisperArchive, (Join-Path $staging 'ffmpeg.sha256'), $whisperOut, $whisperErr -Force -ErrorAction SilentlyContinue
Write-Host 'User PATH updated. New terminals will inherit it; this setup session has it already.'
