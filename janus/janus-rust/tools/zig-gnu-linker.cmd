@echo off
zig cc -target x86_64-windows-gnu %*
exit /b %ERRORLEVEL%
