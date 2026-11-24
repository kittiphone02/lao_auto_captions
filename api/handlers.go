package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true, ".m4v": true,
}

type API struct {
	cfg    Config
	store  *Store
	runner *Runner
}

// Files produced by lao_caption.py next to the uploaded video (it uses Path.with_suffix).
func (a *API) stem(j Job) string        { return filepath.Join(a.store.Dir(j.ID), "video") }
func (a *API) srtPath(j Job) string     { return a.stem(j) + ".lo.srt" }
func (a *API) outPath(j Job) string     { return a.stem(j) + ".captioned.mp4" }
func (a *API) sidecarPath(j Job) string { return a.stem(j) + ".captions.json" }
func (a *API) candidatesPath(j Job) string {
	return filepath.Join(a.stem(j)+".work", "candidates.json")
}

func (a *API) routes(r *gin.Engine) {
	r.GET("/health", a.health)

	v1 := r.Group("/api/v1", a.auth)
	v1.POST("/jobs", a.createJob)
	v1.GET("/jobs", a.listJobs)
	v1.GET("/jobs/:id", a.getJob)
	v1.DELETE("/jobs/:id", a.deleteJob)
	v1.POST("/jobs/:id/cancel", a.cancelJob)
	v1.GET("/jobs/:id/srt", a.getSRT)
	v1.PUT("/jobs/:id/srt", a.putSRT)
	v1.GET("/jobs/:id/segments", a.getSegments)
	v1.PUT("/jobs/:id/segments", a.putSRT)
	v1.POST("/jobs/:id/burn", a.burn)
	v1.GET("/jobs/:id/video", a.getVideo)
	v1.GET("/jobs/:id/source", a.getSource)
	v1.GET("/jobs/:id/waveform", a.getWaveform)
	v1.GET("/sfx", a.listSfx)
	r.GET("/sfx/:name", a.getSfx)

	a.webRoutes(r)
}

// auth also accepts ?key= because a <video src> in the web UI cannot send headers.
func (a *API) auth(c *gin.Context) {
	key := c.GetHeader("X-API-Key")
	if key == "" {
		key = c.Query("key")
	}
	if a.cfg.APIKey != "" && key != a.cfg.APIKey {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or wrong X-API-Key"})
		return
	}
	c.Next()
}

// job loads :id or writes 404.
func (a *API) job(c *gin.Context) (Job, bool) {
	j, err := a.store.Get(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return Job{}, false
	}
	return j, true
}

func isRunning(s Status) bool {
	return s == StatusQueued || s == StatusTranscribing || s == StatusBurning
}

// POST /api/v1/jobs  multipart: video (file), context, no_llm, auto_burn, collect
func (a *API) createJob(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, a.cfg.MaxUploadMB<<20)
	fh, err := c.FormFile("video")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart field 'video' is required: " + err.Error()})
		return
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !videoExts[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type " + ext})
		return
	}

	now := time.Now()
	j := &Job{
		ID:        newID(),
		Filename:  filepath.Base(fh.Filename),
		Video:     "video" + ext,
		Status:    StatusQueued,
		Context:   strings.TrimSpace(c.PostForm("context")),
		NoLLM:     formBool(c, "no_llm"),
		AutoBurn:  formBool(c, "auto_burn"),
		Collect:   formBool(c, "collect"),
		CreatedAt: now,
		UpdatedAt: now,
	}
	// optional caption look for auto_burn, as a JSON string field
	if raw := c.PostForm("style"); raw != "" {
		var st Style
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "style must be JSON: " + err.Error()})
			return
		}
		if err := st.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		j.Style = &st
	}
	dir := a.store.Dir(j.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := c.SaveUploadedFile(fh, filepath.Join(dir, j.Video)); err != nil {
		os.RemoveAll(dir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save upload: " + err.Error()})
		return
	}
	if err := a.store.Add(j); err != nil {
		os.RemoveAll(dir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a.runner.Enqueue(j.ID, taskTranscribe)
	c.JSON(http.StatusAccepted, j)
}

func formBool(c *gin.Context, k string) bool {
	b, _ := strconv.ParseBool(c.PostForm(k))
	return b
}

func (a *API) listJobs(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"jobs": a.store.List()}) }

func (a *API) getJob(c *gin.Context) {
	if j, ok := a.job(c); ok {
		c.JSON(http.StatusOK, j)
	}
}

func (a *API) cancelJob(c *gin.Context) {
	if err := a.store.Cancel(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	j, _ := a.store.Get(c.Param("id"))
	c.JSON(http.StatusOK, j)
}

func (a *API) deleteJob(c *gin.Context) {
	if err := a.store.Delete(c.Param("id")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errNotFound) {
			code = http.StatusNotFound
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /api/v1/jobs/:id/srt  -> the current (possibly edited) .lo.srt
func (a *API) getSRT(c *gin.Context) {
	j, ok := a.job(c)
	if !ok {
		return
	}
	if _, err := os.Stat(a.srtPath(j)); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "captions not ready", "status": j.Status})
		return
	}
	name := strings.TrimSuffix(j.Filename, filepath.Ext(j.Filename)) + ".lo.srt"
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.File(a.srtPath(j))
}

// GET /api/v1/jobs/:id/segments -> captions as JSON, with the raw whisper/w2v candidates
func (a *API) getSegments(c *gin.Context) {
	j, ok := a.job(c)
	if !ok {
		return
	}
	b, err := os.ReadFile(a.srtPath(j))
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "captions not ready", "status": j.Status})
		return
	}
	segs, err := parseSRT(string(b))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// raw whisper/wav2vec2 guesses: match by time, so they survive split/add/delete in the editor
	if cb, err := os.ReadFile(a.candidatesPath(j)); err == nil {
		var cand []Segment
		if json.Unmarshal(cb, &cand) == nil {
			for i := range segs {
				if k := bestOverlap(segs[i], cand); k >= 0 {
					segs[i].Whisper, segs[i].Buzz, segs[i].W2V, segs[i].LLMOK = cand[k].Whisper, cand[k].Buzz, cand[k].W2V, cand[k].LLMOK
				}
			}
		}
	}
	applySidecar(segs, a.sidecarPath(j))
	c.JSON(http.StatusOK, gin.H{"job_id": j.ID, "segments": segs, "style": j.Style})
}

// bestOverlap returns the candidate that overlaps s the most in time, or -1.
func bestOverlap(s Segment, cand []Segment) int {
	best, bestK := 0.0, -1
	for k, c := range cand {
		if ov := min(s.End, c.End) - max(s.Start, c.Start); ov > best {
			best, bestK = ov, k
		}
	}
	return bestK
}

// sidecar is <video>.captions.json: the editor's captions with their sound effects.
// The .srt stays the source of truth for text and timing; a line only keeps its sound
// if its timing still matches (lao_caption.py burn applies the same rule).
type sidecar struct {
	Segments []Segment `json:"segments"`
}

func applySidecar(segs []Segment, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var sc sidecar
	if json.Unmarshal(b, &sc) != nil {
		return
	}
	for i := range segs {
		if i < len(sc.Segments) && near(segs[i].Start, sc.Segments[i].Start) && near(segs[i].End, sc.Segments[i].End) {
			segs[i].Sfx = sc.Segments[i].Sfx
		}
	}
}

func near(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }

// PUT /api/v1/jobs/:id/srt (or /segments)
//
//	Content-Type: application/json  -> {"segments":[{"start":0,"end":1.5,"lao":"..."}]}
//	anything else                   -> raw SRT text
func (a *API) putSRT(c *gin.Context) {
	j, ok := a.job(c)
	if !ok {
		return
	}
	if isRunning(j.Status) {
		c.JSON(http.StatusConflict, gin.H{"error": "job is still running", "status": j.Status})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 10<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var segs []Segment
	var style *Style
	isJSON := strings.HasPrefix(c.ContentType(), "application/json")
	if isJSON {
		var req struct {
			Segments []Segment `json:"segments"`
			Style    *Style    `json:"style"` // optional: lets the editor autosave the look too
		}
		if err := json.Unmarshal(body, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
			return
		}
		segs, style = req.Segments, req.Style
		if style != nil {
			if err := style.Validate(); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		sounds := a.sfxSet()
		for i, s := range segs {
			if s.Sfx != "" && !sounds[s.Sfx] {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("caption %d: unknown sound %q", i+1, s.Sfx)})
				return
			}
		}
	} else if segs, err = parseSRT(string(body)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid SRT: " + err.Error()})
		return
	}
	if len(segs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no captions"})
		return
	}
	for i, s := range segs {
		if s.End <= s.Start || strings.TrimSpace(s.Lao) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("caption %d: empty text or end <= start", i+1)})
			return
		}
	}
	if err := os.WriteFile(a.srtPath(j), []byte(writeSRT(segs)), 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if isJSON {
		side := sidecar{Segments: make([]Segment, len(segs))}
		for i, s := range segs { // keep only what the burn needs
			side.Segments[i] = Segment{Start: s.Start, End: s.End, Lao: s.Lao, Sfx: s.Sfx}
		}
		b, _ := json.MarshalIndent(side, "", " ")
		if err := os.WriteFile(a.sidecarPath(j), b, 0o644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if style != nil {
			_ = a.store.Update(j.ID, func(j *Job) { j.Style = style })
		}
	} else {
		os.Remove(a.sidecarPath(j)) // a raw SRT has no sounds
	}
	c.JSON(http.StatusOK, gin.H{"job_id": j.ID, "captions": len(segs)})
}

// GET /api/v1/sfx -> the sound effects in ../sfx
func (a *API) listSfx(c *gin.Context) {
	names := []string{}
	for n := range a.sfxSet() {
		names = append(names, n)
	}
	sort.Strings(names)
	c.JSON(http.StatusOK, gin.H{"sfx": names})
}

func (a *API) sfxSet() map[string]bool {
	set := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(a.cfg.ScriptDir, "sfx", "*.wav"))
	for _, f := range files {
		set[strings.TrimSuffix(filepath.Base(f), ".wav")] = true
	}
	return set
}

// GET /sfx/:name -> one sound effect, for previewing in the editor
func (a *API) getSfx(c *gin.Context) {
	name := strings.TrimSuffix(c.Param("name"), ".wav")
	if !a.sfxSet()[name] {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.File(filepath.Join(a.cfg.ScriptDir, "sfx", name+".wav"))
}

// GET /api/v1/jobs/:id/waveform?n=1200 -> loudness peaks for drawing the timeline,
// read from the 16 kHz mono audio.wav the transcribe step made.
func (a *API) getWaveform(c *gin.Context) {
	j, ok := a.job(c)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(c.DefaultQuery("n", "1200"))
	n = max(100, min(n, 8000))
	peaks, dur, err := wavPeaks(filepath.Join(a.stem(j)+".work", "audio.wav"), n)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no audio yet: " + err.Error()})
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"duration": dur, "peaks": peaks})
}

// POST /api/v1/jobs/:id/burn  optional JSON {"collect": true}
func (a *API) burn(c *gin.Context) {
	j, ok := a.job(c)
	if !ok {
		return
	}
	if isRunning(j.Status) {
		c.JSON(http.StatusConflict, gin.H{"error": "job is still running", "status": j.Status})
		return
	}
	if _, err := os.Stat(a.srtPath(j)); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no captions to burn yet", "status": j.Status})
		return
	}
	var req struct {
		Collect *bool  `json:"collect"`
		Style   *Style `json:"style"` // omitted = keep the job's current style
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
			return
		}
	}
	if req.Style != nil {
		if err := req.Style.Validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	_ = a.store.Update(j.ID, func(j *Job) {
		j.Status, j.Stage, j.Error = StatusQueued, "burn", ""
		if req.Collect != nil {
			j.Collect = *req.Collect
		}
		if req.Style != nil {
			j.Style = req.Style
		}
	})
	a.runner.Enqueue(j.ID, taskBurn)
	j, _ = a.store.Get(j.ID)
	c.JSON(http.StatusAccepted, j)
}

// GET /api/v1/jobs/:id/video -> the captioned mp4
func (a *API) getVideo(c *gin.Context) {
	j, ok := a.job(c)
	if !ok {
		return
	}
	if _, err := os.Stat(a.outPath(j)); err != nil || isRunning(j.Status) {
		c.JSON(http.StatusConflict, gin.H{"error": "captioned video not ready", "status": j.Status})
		return
	}
	// ?inline=1 lets the web UI play it in a <video> tag instead of downloading
	if c.Query("inline") == "" {
		name := strings.TrimSuffix(j.Filename, filepath.Ext(j.Filename)) + ".captioned.mp4"
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	c.File(a.outPath(j))
}

// GET /api/v1/jobs/:id/source -> the original upload, for previewing while reviewing captions
func (a *API) getSource(c *gin.Context) {
	if j, ok := a.job(c); ok {
		c.File(filepath.Join(a.store.Dir(j.ID), j.Video))
	}
}

// GET /health -> checks python, the script, ffmpeg and the Ollama model
func (a *API) health(c *gin.Context) {
	checks := gin.H{}
	ok := true
	check := func(name string, err error) {
		if err != nil {
			checks[name] = err.Error()
			ok = false
		} else {
			checks[name] = "ok"
		}
	}
	_, err := os.Stat(a.cfg.PythonBin)
	check("python", err)
	_, err = os.Stat(filepath.Join(a.cfg.ScriptDir, "lao_caption.py"))
	check("script", err)
	_, err = exec.LookPath("ffmpeg")
	check("ffmpeg", err)
	check("ollama_model", a.checkOllama(c.Request.Context()))

	code := http.StatusOK
	if !ok {
		code = http.StatusServiceUnavailable
	}
	c.JSON(code, gin.H{"ok": ok, "checks": checks, "model": a.cfg.OllamaModel})
}

func (a *API) checkOllama(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.OllamaURL+"/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable: %w", err)
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return err
	}
	for _, m := range tags.Models {
		if m.Name == a.cfg.OllamaModel || strings.TrimSuffix(m.Name, ":latest") == a.cfg.OllamaModel {
			return nil
		}
	}
	return fmt.Errorf("model %s not pulled (run: ollama pull %s), or use no_llm=true", a.cfg.OllamaModel, a.cfg.OllamaModel)
}
