@echo off
REM Step 2: burn the reviewed .lo.srt into the video, and save the clips as training data
setlocal
cd /d "%~dp0"
if "%~1"=="" ( echo Drag a video file onto burn.bat & pause & exit /b )
call .venv\Scripts\activate.bat
set PYTHONUTF8=1
python lao_caption.py burn "%~1"
python lao_caption.py collect "%~1"
echo.
echo Video: "%~dpn1.captioned.mp4"
pause
