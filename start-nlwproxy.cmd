@echo off
setlocal
pushd "%~dp0" >nul

set "NLWPROXY_EXE=%~dp0dist\nlwproxy.exe"
set "NLWPROXY_CONFIG=%~dp0nlwproxy.json"

rem Route metrics are available from the local /health endpoint while serving.
rem Optional exit-IP probes are configured in observability.exit_ip_probe,
rem cached per route, and never run for each model request.

if not exist "%NLWPROXY_EXE%" (
  echo NlwProxy executable not found: "%NLWPROXY_EXE%"
  echo Build it from PowerShell with:
  echo   go build -trimpath -ldflags="-s -w" -o .\dist\nlwproxy.exe .\cmd\nlwproxy
  popd >nul
  exit /b 1
)

if not exist "%NLWPROXY_CONFIG%" (
  echo NlwProxy configuration not found: "%NLWPROXY_CONFIG%"
  echo Create it with:
  echo   .\dist\nlwproxy.exe init --config .\nlwproxy.json
  popd >nul
  exit /b 1
)

"%NLWPROXY_EXE%" console --config "%NLWPROXY_CONFIG%"
set "NLWPROXY_EXIT=%ERRORLEVEL%"
popd >nul
exit /b %NLWPROXY_EXIT%
