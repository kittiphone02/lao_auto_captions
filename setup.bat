@echo off
setlocal
cd /d "%~dp0"
echo ============================================
echo   Lao Captions - one-time setup (Windows)
echo ============================================

REM ---- 1. Python 3.10-3.12 -------------------------------------------
where python >nul 2>nul
if errorlevel 1 (
  echo Installing Python 3.12 ...
  winget install -e --id Python.Python.3.12 --accept-package-agreements --accept-source-agreements
  echo.
  echo Python installed. CLOSE this window and run setup.bat again.
  pause & exit /b
)

REM ---- 2. ffmpeg ------------------------------------------------------
where ffmpeg >nul 2>nul
if errorlevel 1 (
  echo Installing ffmpeg ...
  winget install -e --id Gyan.FFmpeg --accept-package-agreements --accept-source-agreements
  echo ffmpeg installed. CLOSE this window and run setup.bat again.
  pause & exit /b
)

REM ---- 3. Ollama (runs the Lao LLM) -----------------------------------
where ollama >nul 2>nul
if errorlevel 1 (
  echo Installing Ollama ...
  winget install -e --id Ollama.Ollama --accept-package-agreements --accept-source-agreements
  echo Ollama installed. CLOSE this window and run setup.bat again.
  pause & exit /b
)

REM ---- 4. Python packages in a local venv -----------------------------
if not exist .venv (
  python -m venv .venv
)
call .venv\Scripts\activate.bat
if not exist glossary.txt copy glossary.example.txt glossary.txt >nul
python -m pip install --upgrade pip
pip install torch --index-url https://download.pytorch.org/whl/cpu
pip install -r requirements.txt

REM ---- 5. Download speech models (about 4.5 GB) ----------------------
echo Downloading Whisper large-v3 and Lao wav2vec2 ...
python -c "from faster_whisper import WhisperModel; WhisperModel('large-v3', device='cpu', compute_type='int8')"
python -c "from transformers import Wav2Vec2ForCTC, Wav2Vec2Processor as P; m='Phonepadith/wav2vec2-large-xlsr-53-laos'; P.from_pretrained(m); Wav2Vec2ForCTC.from_pretrained(m)"

REM ---- 6. Download the Lao LLM (about 29 GB) --------------------------
echo Downloading Gemma-SEA-LION-v4-27B (Q8_0) for Ollama ...
ollama pull hf.co/aisingapore/Gemma-SEA-LION-v4-27B-IT-GGUF:Q8_0

echo.
echo ============================================
echo   Setup complete.
echo   Drag a video onto caption.bat to start.
echo ============================================
pause
