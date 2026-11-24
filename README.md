# Lao Captions

Makes Lao captions for a video automatically. You fix the words in a CapCut-style web editor, then export a video with the captions burned in, using your style, animation and sound effects.
Everything runs on your own PC. No video or text is uploaded anywhere.

```
video ─► Whisper large-v3 ─┐  timing + rough text (often Thai letters)
     └► Lao wav2vec2 ──────┤  native Lao letters
                           ▼
          SEA-LION 27B (Ollama) merges both ─► correct Lao
                           ▼
        you review in the editor ─► Export ─► video.captioned.mp4
                           ▼
        your corrections are saved to dataset\ and examples.jsonl (the AI improves over time)
```

There are two ways to use it:
- **Web editor (recommended):** a Go server at http://localhost:8080 with projects, a live preview, a timeline, styles, animations and sound effects.
- **Drag and drop:** `caption.bat` and `burn.bat`, for quick use without the editor.

---

## 1. Setup (one time)

You need Windows 10/11, about **40 GB of free disk space** and **at least 48 GB of RAM** (64 GB recommended). No GPU is needed. If you have an NVIDIA GPU, Ollama uses it automatically and the AI step gets much faster.

1. **Double-click `setup.bat`.** It installs Python, ffmpeg and Ollama with winget, creates `.venv`, and downloads the models (about 35 GB):
   - Whisper large-v3
   - Lao wav2vec2
   - SEA-LION 27B Q8

   If it says *"CLOSE this window and run setup.bat again"*, do that: a newly installed program needs a fresh window.

2. **Install Go** (needed for the web editor), then open a new terminal:
   ```bash
   winget install -e --id GoLang.Go
   ```

3. **Build the server:**
   ```bash
   cd api
   go build -o lao-captions-api.exe .
   ```

### Optional: keep the models on another drive
The models are large. To store them on D: instead of C:, set these **before** running `setup.bat`, then close and reopen the terminal:
```bash
setx OLLAMA_MODELS "D:\ollama\models"
setx HF_HOME "D:\huggingface"
```
Restart Ollama after changing `OLLAMA_MODELS` (quit it from the system tray and start it again).

---

## 2. Use the web editor

### Start
```bash
cd api
./lao-captions-api.exe
```
Open **http://localhost:8080** in your browser and keep the terminal window open while you work.

> Open the address yourself in the browser. If an IDE opens the page for you (for example on port 63343), the page can't reach the server.

The **Ready** dot at the top of the projects page shows whether Python, ffmpeg and the SEA-LION model were found. If one is missing, hover the dot to see which.

### Make captions
1. On **Projects**, drop a video on **New project**.
2. Optionally say what the video is about, for example *"Clothing shop, handmade satin skirts"*. This helps the AI with names and topic words.
3. Click **Create captions**. The project opens and shows its progress: Whisper, then wav2vec2, then SEA-LION. On a CPU this takes about **5–10 minutes per minute of video**. You can close the page; the work continues on the server.

### Edit
| Area | What you do there |
|---|---|
| **Video (middle)** | Live preview of exactly how the export will look: style, line breaks, animation, fade-out. **Drag the caption up or down** to move it. Click the video to play or pause. |
| **Timeline (bottom)** | Click or drag to move through the video. **Drag a caption clip** to move it, and **drag its edges** to trim it. Orange tags are sound effects (click to hear one). The waveform shows where people talk. Ctrl + mouse wheel zooms. |
| **Right panel** | The selected caption: its text, start and end times, and sound effect, plus the AI's raw guesses (Whisper, wav2vec2) to help you fix words. Below that, the **text style for all captions**: size, position, max lines, colours, animation and speed. |
| **Left panel** | **Text:** style presets. **Animation:** none, fade, pop or slide up. **Sound:** effects (pop, click, whoosh, ding, bubble, swipe, boom), **Add to selected** or **Add to all**, and the volume. |
| **Timeline tools** | ✂ **Split** at the playhead, ＋ **Text** (new caption), ⧉ **Duplicate**, 🗑 **Delete**. |

Everything **saves automatically** ("✓ Saved" at the top). **↶ / ↷** undo and redo.

**Keyboard:** Space plays and pauses · ← / → step one frame (Shift for 1 s) · ↑ / ↓ previous or next caption · S splits · Delete removes the caption · Ctrl+Z / Ctrl+Y undo and redo · Ctrl+S saves.

**Long captions:** fast talkers sometimes produce one very long caption. **Max lines** (default 2) shows it in parts of 2 lines, one after another, timed to how much text each part has. To get separate captions you can edit one by one, use ✂ **Split**.

### Export
1. Click **Export**. If a caption still says the placeholder **"ຂໍ້ຄວາມໃໝ່"** from ＋ Text, export stops and jumps to it. Type the words or delete the caption.
2. Tick **Learn from my fixes** to save your corrections as training data and as examples for the AI (see section 4).
3. When it's done, **Result** shows the exported video and **⬇ Download** saves it. **⋯ → Download subtitles (.srt)** saves the captions as a separate file.

Want a different look? Change the style and click **Export again**. Your text isn't touched.

---

## 3. Drag and drop (without the editor)

1. Drag a video onto **`caption.bat`**. It makes `video.lo.srt` next to the video and opens it in Notepad.
2. Fix wrong words and save. Keep the number and timing lines as they are.
3. Drag the same video onto **`burn.bat`**. You get `video.captioned.mp4`, and your fixes are added to `dataset\`.

Tell the AI what the video is about:
```bash
caption.bat "video.mp4" --context "Clothing shop, selling handmade skirts"
```

From the command line you can also choose a caption style:
```bash
.venv\Scripts\python lao_caption.py burn video.mp4 --style "{\"size\":6,\"animation\":\"pop\",\"color\":\"#FFD400\"}"
```
Style keys: `size`, `position`, `color`, `outline_color`, `animation` (`none`/`fade`/`pop`/`slide`), `anim_ms`, `sfx_volume`, `max_lines`.

---

## 4. Make it more accurate over time

- **`glossary.txt`** has the biggest effect for the least work. Add words you use often (shop and product names, colours, sizes, slang), one per line. Whisper and the AI both use them. `setup.bat` creates it from `glossary.example.txt`.
- **Learn from my fixes** (on Export, or `burn.bat`) saves:
  - `examples.jsonl`: pairs of the machine's guess and your correction. The AI reads up to 20 of these on every video.
  - `dataset\clips\*.wav` and `dataset\metadata.csv`: audio clips with the correct text. Once you have about **5–10 hours**, you can fine-tune a speech model on them to get your own Lao model.
- Only tick **Learn from my fixes** after you've checked the words. Whatever you export becomes a lesson.

> **Clean these before you fine-tune anything.** An older version of the editor saved some bad data:
> - `dataset\metadata.csv` has **8 clips labelled "ຂໍ້ຄວາມໃໝ່"** (the placeholder text). Remove those rows and their `.wav` files.
> - `examples.jsonl` **lines 6–11** pair the wrong guess with the wrong correction. Delete those lines.
>
> This can't happen again: export now refuses placeholder text, and corrections are matched to guesses by time.

---

## 5. Settings

Set them in the terminal before starting the server (for example `set API_KEY=secret` in Command Prompt, or `$env:API_KEY="secret"` in PowerShell).

| Setting | Default | What it does |
|---|---|---|
| `ADDR` | `:8080` | Address and port of the web editor and API |
| `DATA_DIR` | `api\data` | Where projects (uploads, captions, exports) are stored |
| `API_KEY` | *(empty)* | If set, the page and API need this key. The page asks for it once. |
| `MAX_UPLOAD_MB` | `2048` | Largest video you can upload |
| `WORKERS` | `1` | Videos processed at the same time. Each extra one loads its own speech models (~5 GB) and shares SEA-LION, so it mostly adds CPU load. Keep 1 on a CPU-only PC. |
| `WORKER_IDLE_MIN` | `30` | The speech models stay loaded between videos and unload after this many idle minutes |
| `OLLAMA_MODEL` | `hf.co/aisingapore/Gemma-SEA-LION-v4-27B-IT-GGUF:Q8_0` | The AI that fixes the Lao |
| `OLLAMA_KEEP_ALIVE` | `30m` | How long SEA-LION stays in memory after a video |
| `OLLAMA_URL` | `http://localhost:11434` | Where Ollama runs |
| `WHISPER_MODEL` | `large-v3` | Speech model for timing and the Thai-script guess |
| `WHISPER_BEAM` | `10` | Whisper search width. Lower is faster but less accurate. |
| `LAO_ASR_MODEL` | *(empty = off)* | Optional extra guess from BuzzASR (run `python get_asr_model.py` first). It was weaker on fast, casual speech, so it's off. |

**Accuracy note:** large-v3 with beam 10, wav2vec2 and SEA-LION Q8 is the setup that gave the best captions. A test with `large-v3-turbo`, beam 5 and BuzzASR was about 4× faster at the Whisper step, but the final Lao was clearly worse.

---

## 6. Speed and resources (Intel i9-14th gen, 64 GB RAM, CPU only)

| Step | 1-minute video | RAM |
|---|---|---|
| Whisper large-v3 (beam 10) | about 2–3 min | ~3 GB |
| Lao wav2vec2 | about 20 s | ~1.5 GB |
| SEA-LION 27B Q8 | about 3–10 min | ~29 GB |
| Export (burn) | about the length of the video | small |

The first video after starting the server also loads the models (about 1 minute). After that they stay loaded.

**Ways to make it faster without changing accuracy:**
- A **GPU** (for example 24 GB, like an RTX 4090 or a cloud L4/A10) makes every step 10–20× faster.
- For a public deployment, run the web server on a small machine and the processing on a separate GPU machine.

---

## 7. API and Postman

The web editor uses a REST API that you can call from your own apps:
- Endpoint list, caption-style options and examples: [`api/README.md`](api/README.md).
- Postman: import both files from `api/postman/`. **Create job** saves the new job id automatically for the other requests.

Short version:
```bash
curl -F video=@shop.mp4 -F context="Clothing shop" http://localhost:8080/api/v1/jobs
curl http://localhost:8080/api/v1/jobs/<id>                 # poll until "status":"review"
curl -X POST http://localhost:8080/api/v1/jobs/<id>/burn
curl -o shop.captioned.mp4 http://localhost:8080/api/v1/jobs/<id>/video
```

---

## 8. Troubleshooting

| Problem | Fix |
|---|---|
| Page shows "server not reachable", or requests go to port 63343 | Start `lao-captions-api.exe` and open **http://localhost:8080** yourself. Don't use an IDE's preview link. |
| Ready dot is red: `ollama_model` | Start Ollama, then run `ollama pull hf.co/aisingapore/Gemma-SEA-LION-v4-27B-IT-GGUF:Q8_0` |
| Ready dot is red: `python` or `script` | Run `setup.bat` again. Start the server from the `api` folder. |
| No captions: "No speech was found" | The video is music only, or very quiet. |
| A word is always wrong (a name, a product) | Add it to `glossary.txt`. |
| Changed the code or `web/index.html` but nothing changed | Stop the server (Ctrl+C), rebuild (`go build -o lao-captions-api.exe .`), start it again, then press Ctrl+F5 in the browser. |
| "The process cannot access the file" when building | The server is still running. Stop it first. |
| A job says "server restarted while running" | The server stopped mid-job. Open the project and Export again, or create the project again if it was still making captions. |
| Very slow, or the PC freezes | Another video, or another program, is using the RAM. Process one video at a time (`WORKERS=1`). |

---

## 9. Folder layout

| Path | What it is |
|---|---|
| `lao_caption.py` | The pipeline: transcribe, AI fix, burn, collect. Also the `serve` worker used by the server. |
| `caption.bat`, `burn.bat`, `setup.bat` | Drag-and-drop use and one-time setup |
| `glossary.txt`, `examples.jsonl` | Your words, and your past corrections for the AI (not in git; `glossary.example.txt` is the starting point) |
| `dataset\` | Clips and text for training your own model later (private, not in git) |
| `fonts\` | Noto Sans Lao (used for export and preview) |
| `sfx\`, `make_sfx.py` | Sound effects; the script rebuilds them |
| `get_asr_model.py`, `models\` | Optional BuzzASR model (off by default) |
| `api\` | Go server, web editor (`web/index.html`), Postman collection, API README |
| `api\data\jobs\<id>\` | One folder per project: the uploaded video, `.lo.srt`, `.captions.json` (sound effects), the export (private, not in git) |

---

## 10. Licenses: check before commercial use

- Whisper: MIT
- Gemma-SEA-LION-v4: Gemma Terms of Use
- `Phonepadith/wav2vec2-large-xlsr-53-laos`: proprietary (AIDCTECH). Contact the publisher before you use it in a product.
- BuzzASR/lao (optional, off): MIT
- Gin (Go web framework): MIT
- FFmpeg: LGPL/GPL
- Noto Sans Lao: SIL Open Font License
- Sound effects in `sfx\`: synthesized by `make_sfx.py`, free to use
