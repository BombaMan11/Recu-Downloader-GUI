@echo off
rem Builds Recu.exe in this folder. First change vX.X.X to your version number.
go build -trimpath -ldflags="-s -w -H=windowsgui -X main.tag=v3.1.2" -buildvcs=false -o Recu.exe
if errorlevel 1 (
  echo.
  echo Build failed. Read the message above.
) else (
  echo.
  echo Built Recu.exe
)
pause
