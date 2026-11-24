package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Config is read from environment variables. Defaults assume the api/ folder
// sits next to lao_caption.py and its .venv.
type Config struct {
	Addr        string        // ADDR, default ":8080"
	ScriptDir   string        // SCRIPT_DIR, folder holding lao_caption.py (default: parent of api/)
	PythonBin   string        // PYTHON_BIN, default <ScriptDir>/.venv/Scripts/python.exe
	DataDir     string        // DATA_DIR, where uploads and job files live
	Workers     int           // WORKERS, parallel pipeline runs (default 1: models are CPU/RAM heavy)
	WorkerIdle  time.Duration // WORKER_IDLE_MIN, unload the speech models after this long without jobs (default 30)
	MaxUploadMB int64         // MAX_UPLOAD_MB, default 2048
	APIKey      string        // API_KEY, if set every /api request needs header X-API-Key
	OllamaURL   string        // OLLAMA_URL, only used by /health
	OllamaModel string        // OLLAMA_MODEL, only used by /health
}

func loadConfig() Config {
	wd, _ := os.Getwd()
	scriptDir := env("SCRIPT_DIR", filepath.Dir(wd))
	if abs, err := filepath.Abs(scriptDir); err == nil {
		scriptDir = abs
	}

	py := filepath.Join(scriptDir, ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		py = filepath.Join(scriptDir, ".venv", "Scripts", "python.exe")
	}

	return Config{
		Addr:        env("ADDR", ":8080"),
		ScriptDir:   scriptDir,
		PythonBin:   env("PYTHON_BIN", py),
		DataDir:     env("DATA_DIR", filepath.Join(wd, "data")),
		Workers:     envInt("WORKERS", 1),
		WorkerIdle:  time.Duration(envInt("WORKER_IDLE_MIN", 30)) * time.Minute,
		MaxUploadMB: int64(envInt("MAX_UPLOAD_MB", 2048)),
		APIKey:      os.Getenv("API_KEY"),
		OllamaURL:   env("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel: env("OLLAMA_MODEL", "hf.co/aisingapore/Gemma-SEA-LION-v4-27B-IT-GGUF:Q8_0"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}
