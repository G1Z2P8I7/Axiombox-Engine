@echo off
title AxiomBox - Stopping All Services
echo ==================================================================
echo           AXIOMBOX -- STOPPING ALL SERVICES                      
echo ==================================================================
echo.

echo Stopping axiombox.exe backend...
taskkill /F /IM axiombox.exe 2>nul
if errorlevel 1 (
    echo [OK] Backend was not running.
) else (
    echo [OK] Backend process terminated.
)

echo Stopping Next.js playground...
taskkill /F /IM node.exe /FI "WINDOWTITLE eq AxiomBox Frontend*" 2>nul

echo.
echo All AxiomBox services stopped cleanly.
pause
