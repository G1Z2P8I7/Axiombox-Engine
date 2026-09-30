@echo off
title AxiomBox - Starting All Services
echo ==================================================================
echo           AXIOMBOX -- KERNEL-ISOLATED RCE SANDBOX ENGINE           
echo ==================================================================
echo.

:: Ensure current directory is the script directory
cd /d "%~dp0"

echo [1/3] Checking backend binary...
if not exist "axiombox.exe" (
    echo Compiling axiombox.exe from Go source...
    go build -o axiombox.exe .\cmd\axiombox
    if errorlevel 1 (
        echo [ERROR] Failed to compile backend. Check Go installation.
        pause
        exit /b 1
    )
)

echo [2/3] Launching AxiomBox Go Backend Engine on port 8080...
start "AxiomBox Backend Engine (Port 8080)" cmd /k "cd /d ""%~dp0"" && .\axiombox.exe -port 8080 -concurrency 20"

:: Wait 2 seconds for backend to bind port
timeout /t 2 /nobreak >nul

echo [3/3] Launching AxiomBox Next.js Playground on port 3000...
start "AxiomBox Frontend Playground (Port 3000)" cmd /k "cd /d ""%~dp0frontend\playground"" && npm run dev"

:: Wait 3 seconds for Next.js server initialization
timeout /t 3 /nobreak >nul

echo Opening web browser to http://localhost:3000 ...
start http://localhost:3000

echo.
echo ==================================================================
echo  ALL SERVICES LAUNCHED SUCCESSFULLY!
echo   - Win32 Kernel Backend: http://localhost:8080
echo   - Interactive Web UI:   http://localhost:3000
echo ==================================================================
echo  To shut down, simply close the two opened command prompt windows
echo  or run stopall.bat.
echo.
pause
