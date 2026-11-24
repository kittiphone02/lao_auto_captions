// Lao captions HTTP API: wraps ../lao_caption.py (Whisper + Lao wav2vec2 + SEA-LION via Ollama)
// as a job-based Gin service. See README.md in this folder for the endpoints.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := loadConfig()
	if _, err := os.Stat(cfg.PythonBin); err != nil {
		log.Printf("warning: python not found at %s (set PYTHON_BIN)", cfg.PythonBin)
	}

	store, err := newStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	runner := newRunner(cfg, store)
	runner.Resume()

	r := gin.Default()
	r.MaxMultipartMemory = 32 << 20 // larger uploads spill to temp files
	(&API{cfg: cfg, store: store, runner: runner}).routes(r)

	log.Printf("script dir: %s", cfg.ScriptDir)
	log.Printf("data dir:   %s", cfg.DataDir)
	log.Printf("listening on %s (%d worker)", cfg.Addr, cfg.Workers)
	if err := r.Run(cfg.Addr); err != nil {
		log.Fatal(err)
	}
}
