@echo off
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0zig-linker.ps1" %*
exit /b %ERRORLEVEL%
