@echo off
rem Builds Recu.exe in this folder. Change v3.X.X to your version number first.
go build -trimpath -ldflags="-H=windowsgui -s -w -X main.tag=v3.2.2" -buildvcs=false -o Recu.exe
if errorlevel 1 (
  echo.
  echo Build failed. Read the message above.
) else (
  echo.
  echo Built Recu.exe
)
pause
