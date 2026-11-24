# Lao Captions API (Go + Gin)

An HTTP service around `../lao_caption.py`. Every pipeline step runs your existing Python script from `../.venv`:
Whisper large-v3 → Lao wav2vec2 → SEA-LION via Ollama → burn with Noto Sans Lao.

```
POST video ─► queued ─► transcribing (whisper → wav2vec2 → llm) ─► review
                                                                    │  GET/PUT srt or segments (fix words)
                                                                    ▼
                                         POST burn ─► burning (burn → collect) ─► done ─► GET video
```

## Run

```bash
cd api
go build -o lao-captions-api.exe .
./lao-captions-api.exe
```

Run `setup.bat` once first. `/health` reports any missing piece, for example an Ollama model that hasn't been pulled.

| Env var | Default |
|---|---|
| `ADDR` | `:8080` |
| `SCRIPT_DIR` | parent folder (where `lao_caption.py` is) |
| `PYTHON_BIN` | `<SCRIPT_DIR>\.venv\Scripts\python.exe` |
| `DATA_DIR` | `.\data` (uploads + outputs, one folder per job) |
| `WORKERS` | `1` (the models are heavy, so jobs run one at a time) |
| `WORKER_IDLE_MIN` | `30`: the speech models stay loaded between videos and are unloaded after this many idle minutes |
| `WHISPER_BEAM` | `10`: Whisper's search width. Lower is faster, but a test with 5 (and turbo) gave worse Lao |
| `OLLAMA_KEEP_ALIVE` | `30m`: how long Ollama keeps SEA-LION in memory after a video |
| `MAX_UPLOAD_MB` | `2048` |
| `API_KEY` | empty = no auth. If set, send header `X-API-Key` |
| `OLLAMA_MODEL`, `OLLAMA_URL`, `WHISPER_MODEL` | passed through to the Python script |
| `LAO_ASR_MODEL` | empty (off). Set it to `..\models\buzzasr-lao-ct2` to add BuzzASR as an extra hint (run `get_asr_model.py` first); it was weaker on fast casual speech |

## Endpoints

| Method | Path | What it does |
|---|---|---|
| GET | `/health` | Checks python, script, ffmpeg and the Ollama model |
| POST | `/api/v1/jobs` | Multipart: `video` (file), `context`, `no_llm`, `auto_burn`, `collect` → `202` + job |
| GET | `/api/v1/jobs` | Lists jobs, newest first |
| GET | `/api/v1/jobs/:id` | Returns status, stage, error and last log lines |
| GET | `/api/v1/jobs/:id/srt` | Downloads `.lo.srt` |
| PUT | `/api/v1/jobs/:id/srt` | Replaces the captions with a raw SRT body |
| GET | `/api/v1/jobs/:id/segments` | Captions as JSON (with each one's `sfx`), the raw `whisper` / `w2v` guesses, and the job's `style` |
| PUT | `/api/v1/jobs/:id/segments` | `{"segments":[{"start":0,"end":2.1,"lao":"...","sfx":"pop"}], "style":{...}}`; `sfx` and `style` are optional |
| GET | `/api/v1/jobs/:id/waveform?n=1200` | Loudness peaks for drawing a timeline |
| GET | `/api/v1/sfx` | Names of the available sound effects |
| GET | `/sfx/:name.wav` | One sound effect, for previewing |
| POST | `/api/v1/jobs/:id/burn` | Queues the burn. Optional body: `{"collect":true}` also saves clips to `dataset\` and `examples.jsonl`; `"style":{...}` sets the caption look (see below) |
| GET | `/api/v1/jobs/:id/video` | Downloads the captioned mp4 |
| POST | `/api/v1/jobs/:id/cancel` | Stops a queued or running job |
| DELETE | `/api/v1/jobs/:id` | Cancels the job and removes its files |

### Caption style

Send `style` in the burn body, or as a JSON string form field `style` when creating a job with `auto_burn=true`. Every key is optional.

| Key | Default | Meaning |
|---|---|---|
| `size` | `3.7` | Font size, % of video height (1.5–12) |
| `position` | `13` | Gap from the bottom, % of video height (2–60) |
| `color` | `#FFFFFF` | Text colour |
| `outline_color` | `#000000` | Outline colour |
| `animation` | `none` | `none`, `fade` (in and out), `pop` (grows in), `slide` (rises in) |
| `anim_ms` | `300` | Animation length in ms (50–2000) |
| `sfx_volume` | `1` | Loudness of the sound effects (0–2) |
| `max_lines` | `2` | A caption that wraps to more lines is shown as pages of this many lines, one after another; each page gets a share of the caption's time by its length (1–6) |

### Sound effects

Set `sfx` on a caption (with PUT `/segments`) to play a sound when it appears: `pop`, `click`, `whoosh`, `ding`, `bubble`, `swipe` or `boom`. On export, each sound is mixed into the video's audio at its caption's start. The sounds are in `../sfx/` and were synthesized by `../make_sfx.py`, so they are free to use. To add your own, drop a short `.wav` into `../sfx/`.

Sounds are saved next to the video in `video.captions.json`. A caption keeps its sound only while its timing matches the `.srt`, so editing the `.srt` by hand never moves a sound to the wrong line. Uploading a raw SRT with PUT `/srt` removes all sounds.

```json
{ "collect": false, "style": { "size": 6, "position": 20, "color": "#FFD400", "animation": "pop", "anim_ms": 500 } }
```

The job remembers its style, so a later burn without `style` reuses it. From the command line: `python lao_caption.py burn video.mp4 --style "{\"size\":6,\"animation\":\"fade\"}"`.

Status values: `queued`, `transcribing`, `review`, `burning`, `done`, `failed`, `canceled`.
Stage values: `whisper`, `wav2vec2`, `llm`, `burn`, `collect`.

## Web UI

Start the server and open **http://localhost:8080** in your browser. It works like CapCut.

**Projects:** drop a video on **New project**, optionally say what it's about, then click **Create captions**. The project opens and shows the steps (Whisper → wav2vec2 → SEA-LION) until the captions are ready.

**Editor:**
- **Left panel:** **Text** has style presets, **Animation** has fade / pop / slide up and a speed slider, and **Sound** has the effects (click one to hear it, then **Add to selected** or **Add to all**) and their volume.
- **Middle:** the video with the captions drawn live, exactly as the export will look: size, line breaks, animation and fade-out, for every frame while you play or scrub. Sound effects play as each caption appears. **Drag the caption up or down** to move it.
- **Right panel:** the selected caption's text, start/end time and sound, the AI's raw guesses, and the text style for all captions.
- **Timeline:** click or drag on it to scrub. Drag a caption clip to move it, and drag its edges to trim it. Orange tags are sounds (click one to hear it). The waveform shows where people talk. Ctrl + mouse wheel zooms.
- **Tools:** ✂ Split at the playhead, ＋ Text (new caption at the playhead), ⧉ Duplicate, 🗑 Delete, and ↶ / ↷ Undo / Redo.
- **Autosave:** every change saves by itself ("✓ Saved" at the top).
- **Export:** draws the captions and mixes in the sounds. Tick **Learn from my fixes** to add your corrections to `dataset\` and `examples.jsonl`. When it's done, **Result** shows the exported video and **⬇ Download** saves it.

Keyboard: Space plays and pauses, ← / → step one frame (Shift for 1 s), ↑ / ↓ jump to the previous or next caption, S splits, Delete removes a caption, Ctrl+Z / Ctrl+Y undo and redo, Ctrl+S saves.

The page is built into the `.exe` (`web/index.html`), so there is nothing extra to install. If `API_KEY` is set, the page asks for it once.

## Postman

Import both files in `postman/` (Postman → Import):
- `lao-captions-api.postman_collection.json` has every endpoint, in order. Create job saves the new id into `{{jobId}}` automatically.
- `lao-captions-local.postman_environment.json` sets `baseUrl` (default `http://localhost:8080`) and `apiKey`.

## Example

```bash
curl -F video=@shop.mp4 -F context="Clothing shop, handmade skirts" http://localhost:8080/api/v1/jobs
curl http://localhost:8080/api/v1/jobs/<id>                      # poll until "status":"review"
curl http://localhost:8080/api/v1/jobs/<id>/segments              # review
curl -X PUT -H "Content-Type: application/json" --data-binary @fixed.json http://localhost:8080/api/v1/jobs/<id>/segments
curl -X POST -H "Content-Type: application/json" -d '{"collect":true}' http://localhost:8080/api/v1/jobs/<id>/burn
curl -o shop.captioned.mp4 http://localhost:8080/api/v1/jobs/<id>/video
```

To skip the review step, send `auto_burn=true`. The job then goes straight to `done`.

## Notes

- Jobs run in one long-running `lao_caption.py serve` process, so the speech models (about 7 GB) load once and stay in memory between videos. After `WORKER_IDLE_MIN` minutes without jobs it exits to free the memory; the next job starts it again. Canceling a job also restarts it.
- If the server restarts, queued jobs run again. A job that was mid-run is marked `failed`, and you can retry it with `burn`, or re-upload if it was still transcribing.
