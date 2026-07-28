@echo off
setlocal
pushd "%~dp0" >nul
for /f "tokens=2,*" %%A in ('reg query HKCU\Environment /v NLW_PROXY_LOCAL_TOKEN 2^>nul ^| findstr /i NLW_PROXY_LOCAL_TOKEN') do set "NLW_PROXY_LOCAL_TOKEN=%%B"
for /f "tokens=2,*" %%A in ('reg query HKCU\Environment /v REFFAUNLIMITED_API_KEY 2^>nul ^| findstr /i REFFAUNLIMITED_API_KEY') do set "REFFAUNLIMITED_API_KEY=%%B"
if not defined NLW_PROXY_LOCAL_TOKEN exit /b 1
if not defined REFFAUNLIMITED_API_KEY exit /b 1
"%~dp0dist\nlwproxy.exe" serve --config "%~dp0nlwproxy.json"
set "NLWPROXY_EXIT=%ERRORLEVEL%"
popd >nul
exit /b %NLWPROXY_EXIT%
