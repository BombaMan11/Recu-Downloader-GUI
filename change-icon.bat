@echo off
rem Turns winres\icon.png into the program icon, then run build.bat.
go-winres make
if errorlevel 1 (
  echo.
  echo go-winres failed. See "Changing the icons" in the README.
) else (
  echo.
  echo Icon files written. Now run build.bat.
)
pause
