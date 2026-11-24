@echo off
REM Step 1: transcribe a video -> makes <video>.lo.srt for you to review
REM Usage: drag a video file onto this .bat, or:  caption.bat "C:\path\video.mp4"
setlocal
cd /d "%~dp0"
if "%~1"=="" ( echo Drag a video file onto caption.bat & pause & exit /b )
call .venv\Scripts\activate.bat
set PYTHONUTF8=1
python lao_caption.py transcribe "%~1" %2 %3 %4
echo.
echo Open "%~dpn1.lo.srt" in Notepad / Subtitle Edit, fix any words, save,
echo then drag the same video onto burn.bat
start "" notepad "%~dpn1.lo.srt"
pause
