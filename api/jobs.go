package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued       Status = "queued"       // waiting for a worker
	StatusTranscribing Status = "transcribing" // whisper -> wav2vec2 -> LLM
	StatusReview       Status = "review"       // .lo.srt ready, waiting for corrections + burn
	StatusBurning      Status = "burning"      // ffmpeg burn (+ collect)
	StatusDone         Status = "done"         // captioned video ready
	StatusFailed       Status = "failed"
	StatusCanceled     Status = "canceled"
)

const maxLogLines = 200

type Job struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"` // original upload name
	Video     string    `json:"video"`    // stored file name inside the job dir, e.g. video.mp4
	Status    Status    `json:"status"`
	Stage     string    `json:"stage,omitempty"` // whisper | wav2vec2 | llm | burn | collect
	Context   string    `json:"context,omitempty"`
	NoLLM     bool      `json:"no_llm"`
	AutoBurn  bool      `json:"auto_burn"`
	Collect   bool      `json:"collect"`
	Style     *Style    `json:"style,omitempty"` // caption look used by the last/next burn
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Log       []string  `json:"log,omitempty"`

	cancel context.CancelFunc
}

// Store keeps jobs in memory and mirrors each one to <dataDir>/jobs/<id>/job.json
// so they survive a restart.
type Store struct {
	mu   sync.Mutex
	dir  string
	jobs map[string]*Job
}

var errNotFound = errors.New("job not found")

func newStore(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, jobs: map[string]*Job{}}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "job.json"))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) == nil && j.ID != "" {
			if j.Video == "" { // job.json written before the field was saved
				for ext := range videoExts {
					if _, err := os.Stat(filepath.Join(dir, e.Name(), "video"+ext)); err == nil {
						j.Video = "video" + ext
					}
				}
			}
			s.jobs[j.ID] = &j
		}
	}
	return s, nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) Dir(id string) string { return filepath.Join(s.dir, id) }

func (s *Store) Add(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	return s.saveLocked(j)
}

// Get returns a copy so callers can read it without holding the lock.
func (s *Store) Get(id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, errNotFound
	}
	c := *j
	c.Log = append([]string(nil), j.Log...)
	return c, nil
}

func (s *Store) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		c := *j
		c.Log = nil
		out = append(out, c)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out
}

// Update applies fn under the lock and persists the result.
func (s *Store) Update(id string, fn func(*Job)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return errNotFound
	}
	fn(j)
	j.UpdatedAt = time.Now()
	return s.saveLocked(j)
}

// AppendLog adds a line without writing job.json every time (the next Update persists it).
func (s *Store) AppendLog(id, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Log = append(j.Log, line)
		if len(j.Log) > maxLogLines {
			j.Log = j.Log[len(j.Log)-maxLogLines:]
		}
	}
}

func (s *Store) SetCancel(id string, c context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.cancel = c
	}
}

func (s *Store) Cancel(id string) error {
	s.mu.Lock()
	j, ok := s.jobs[id]
	var cancel context.CancelFunc
	if ok {
		cancel = j.cancel
	}
	s.mu.Unlock()
	if !ok {
		return errNotFound
	}
	if cancel != nil {
		cancel()
	}
	return s.Update(id, func(j *Job) {
		if j.Status == StatusQueued || j.Status == StatusTranscribing || j.Status == StatusBurning {
			j.Status = StatusCanceled
			j.Stage = ""
		}
	})
}

func (s *Store) Delete(id string) error {
	if err := s.Cancel(id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
	return os.RemoveAll(s.Dir(id))
}

func (s *Store) saveLocked(j *Job) error {
	b, err := json.MarshalIndent(j, "", " ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir(j.ID), "job.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.Dir(j.ID), "job.json"))
}
