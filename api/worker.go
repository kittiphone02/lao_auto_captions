package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type taskKind string

const (
	taskTranscribe taskKind = "transcribe"
	taskBurn       taskKind = "burn"
)

type task struct {
	jobID string
	kind  taskKind
}

// Runner executes lao_caption.py for queued tasks. Whisper + wav2vec2 + a 27B LLM
// are heavy, so the default is one worker processing tasks in order.
type Runner struct {
	cfg   Config
	store *Store
	queue chan task
}

func newRunner(cfg Config, store *Store) *Runner {
	r := &Runner{cfg: cfg, store: store, queue: make(chan task, 1024)}
	for i := 0; i < cfg.Workers; i++ {
		go (&lane{Runner: r}).loop()
	}
	return r
}

func (r *Runner) Enqueue(id string, kind taskKind) { r.queue <- task{id, kind} }

// Resume re-queues jobs that were waiting when the server stopped and fails the
// ones that were mid-run (their child process is gone).
func (r *Runner) Resume() {
	for _, j := range r.store.List() {
		switch j.Status {
		case StatusQueued:
			// a job queued for burning keeps Stage "burn" (see handleBurn)
			if j.Stage == "burn" {
				r.Enqueue(j.ID, taskBurn)
			} else {
				r.Enqueue(j.ID, taskTranscribe)
			}
		case StatusTranscribing, StatusBurning:
			_ = r.store.Update(j.ID, func(j *Job) {
				j.Status, j.Stage, j.Error = StatusFailed, "", "server restarted while running"
			})
		}
	}
}

// lane is one worker goroutine with its own long-running `lao_caption.py serve` process,
// which keeps the speech models loaded between videos. It is stopped after WORKER_IDLE_MIN
// minutes without work to give the memory back, and started again on the next job.
type lane struct {
	*Runner
	py *pyWorker
}

func (l *lane) loop() {
	for {
		select {
		case t := <-l.queue:
			j, err := l.store.Get(t.jobID)
			if err != nil || j.Status == StatusCanceled {
				continue
			}
			switch t.kind {
			case taskTranscribe:
				l.transcribe(j)
			case taskBurn:
				l.burn(j)
			}
		case <-time.After(l.cfg.WorkerIdle):
			if l.py != nil {
				log.Printf("worker idle for %v: unloading models", l.cfg.WorkerIdle)
				l.py.stop()
				l.py = nil
			}
		}
	}
}

func (r *Runner) videoPath(j Job) string { return filepath.Join(r.store.Dir(j.ID), j.Video) }

// request is one line sent to `lao_caption.py serve`.
type request struct {
	Command string `json:"command"`
	Video   string `json:"video"`
	NoLLM   bool   `json:"no_llm,omitempty"`
	Context string `json:"context,omitempty"`
	Style   string `json:"style,omitempty"`
}

func (l *lane) transcribe(j Job) {
	l.set(j.ID, StatusTranscribing, "whisper")
	req := request{Command: "transcribe", Video: l.videoPath(j), NoLLM: j.NoLLM, Context: j.Context}
	if err := l.run(j.ID, req); err != nil {
		l.fail(j.ID, err)
		return
	}
	l.set(j.ID, StatusReview, "")
	if j.AutoBurn {
		l.burn(j)
	}
}

func (l *lane) burn(j Job) {
	l.set(j.ID, StatusBurning, "burn")
	req := request{Command: "burn", Video: l.videoPath(j)}
	if j.Style != nil {
		req.Style = j.Style.Arg()
	}
	if err := l.run(j.ID, req); err != nil {
		l.fail(j.ID, err)
		return
	}
	if j.Collect {
		l.set(j.ID, StatusBurning, "collect")
		// collect only feeds the training dataset; a failure there should not lose the video
		if err := l.run(j.ID, request{Command: "collect", Video: l.videoPath(j)}); err != nil {
			l.store.AppendLog(j.ID, "collect failed: "+err.Error())
		}
	}
	l.set(j.ID, StatusDone, "")
}

func (r *Runner) set(id string, st Status, stage string) {
	_ = r.store.Update(id, func(j *Job) {
		if j.Status != StatusCanceled {
			j.Status, j.Stage, j.Error = st, stage, ""
		}
	})
}

func (r *Runner) fail(id string, err error) {
	_ = r.store.Update(id, func(j *Job) {
		if j.Status != StatusCanceled {
			j.Status, j.Error = StatusFailed, err.Error()
		}
	})
}

// run sends one job to the serve process and streams its output into the job log,
// watching for the script's "[n/3]" progress lines to update the stage.
// Canceling kills the process (models are reloaded on the next job).
func (l *lane) run(id string, req request) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.store.SetCancel(id, cancel)
	defer l.store.SetCancel(id, nil)

	if l.py == nil {
		py, err := startPyWorker(l.cfg)
		if err != nil {
			return err
		}
		l.py = py
	}
	l.store.AppendLog(id, "$ lao_caption.py "+req.Command)
	b, _ := json.Marshal(req)
	if _, err := l.py.stdin.Write(append(b, '\n')); err != nil {
		l.py.stop()
		l.py = nil
		return fmt.Errorf("send job to python: %w", err)
	}

	var tail []string
	for {
		select {
		case <-ctx.Done():
			l.py.stop()
			l.py = nil
			return errors.New("canceled")
		case line, ok := <-l.py.lines:
			if !ok { // the process died (crash, out of memory...)
				l.py.stop()
				l.py = nil
				return fmt.Errorf("python worker stopped: %s", strings.Join(tail, " | "))
			}
			switch {
			case line == "@@DONE":
				return nil
			case strings.HasPrefix(line, "@@ERROR"):
				log.Printf("job %s: %s", id, line)
				return fmt.Errorf("%s: %s", strings.TrimSpace(strings.TrimPrefix(line, "@@ERROR")), strings.Join(tail, " | "))
			}
			l.store.AppendLog(id, line)
			if stage := stageFromLine(line); stage != "" {
				_ = l.store.Update(id, func(j *Job) { j.Stage = stage })
			}
			tail = append(tail, line)
			if len(tail) > 5 {
				tail = tail[1:]
			}
		}
	}
}

// pyWorker is a running `python lao_caption.py serve`.
type pyWorker struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string // stdout+stderr, one line at a time; closed when the process exits
}

func startPyWorker(cfg Config) (*pyWorker, error) {
	script := filepath.Join(cfg.ScriptDir, "lao_caption.py")
	cmd := exec.Command(cfg.PythonBin, "-u", script, "serve")
	cmd.Dir = cfg.ScriptDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONUNBUFFERED=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start python: %w", err)
	}
	p := &pyWorker{cmd: cmd, stdin: stdin, lines: make(chan string, 256)}
	go func() {
		defer close(p.lines)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			// progress bars redraw with \r; keep only the final state
			line := strings.TrimRight(sc.Text(), "\r")
			if i := strings.LastIndex(line, "\r"); i >= 0 {
				line = line[i+1:]
			}
			if line != "" {
				p.lines <- line
			}
		}
	}()
	go func() { _ = cmd.Wait(); pw.Close() }()

	// wait for the script to be ready (imports done) so a broken setup fails fast
	timeout := time.After(2 * time.Minute)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				return nil, errors.New("python worker exited at start (check PYTHON_BIN and the .venv)")
			}
			if line == "@@READY" {
				log.Printf("python worker started (pid %d)", cmd.Process.Pid)
				return p, nil
			}
		case <-timeout:
			p.stop()
			return nil, errors.New("python worker did not start within 2 minutes")
		}
	}
}

func (p *pyWorker) stop() {
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	for range p.lines { // drain so the reader goroutine can finish
	}
}

func stageFromLine(line string) string {
	switch {
	case strings.HasPrefix(line, "[1/3]"):
		return "whisper"
	case strings.HasPrefix(line, "[2/3]"):
		return "wav2vec2"
	case strings.HasPrefix(line, "[3/3]"):
		return "llm"
	}
	return ""
}
