"""
Lao auto-caption pipeline.

  1. Clean audio (ffmpeg)            -> 16 kHz mono, normalised
  2. Whisper large-v3                 -> timing + rough text (often Thai script)
  3. Lao wav2vec2 (CTC)               -> second opinion, native Lao script
  4. Local LLM via Ollama (SEA-LION)  -> merges both, outputs correct Lao
  5. You review the .srt              -> fix anything still wrong
  6. Burn captions (Noto Sans Lao)    -> final video
  7. Collect corrected clips          -> dataset for fine-tuning your own model later

Usage:
  python lao_caption.py transcribe video.mp4            # makes video.lo.srt (+ candidates json)
  python lao_caption.py burn video.mp4                  # burns video.lo.srt into video.captioned.mp4
  python lao_caption.py collect video.mp4               # adds corrected clips to ./dataset
  python lao_caption.py auto video.mp4                  # transcribe + burn in one go (no review)
  python lao_caption.py serve                           # job loop on stdin, models stay loaded (API)
"""
import argparse
import csv
import json
import os
import re
import shutil
import subprocess
import sys
import unicodedata
from pathlib import Path

import numpy as np
import requests
import soundfile as sf

HERE = Path(__file__).resolve().parent
SR = 16000

# ---------------------------------------------------------------- config
# Whisper's Thai-script text is the LLM's best clue, so large-v3 with beam 10 stays the default:
# a test with large-v3-turbo / beam 5 was ~4x faster but gave clearly worse final Lao.
WHISPER_MODEL = os.environ.get("WHISPER_MODEL", "large-v3")
WHISPER_BEAM = int(os.environ.get("WHISPER_BEAM", "10"))
# Optional extra Lao guess from BuzzASR/lao (made by get_asr_model.py), e.g.
# LAO_ASR_MODEL=models\buzzasr-lao-ct2. Off by default: on casual fast speech it was weaker than
# wav2vec2 and made the final text worse, and it adds minutes per video.
LAO_ASR_MODEL = os.environ.get("LAO_ASR_MODEL", "")
W2V_MODEL = "Phonepadith/wav2vec2-large-xlsr-53-laos"  # check its license before commercial use
OLLAMA_URL = os.environ.get("OLLAMA_URL", "http://localhost:11434")
OLLAMA_MODEL = os.environ.get("OLLAMA_MODEL", "hf.co/aisingapore/Gemma-SEA-LION-v4-27B-IT-GGUF:Q8_0")
# how long Ollama keeps the LLM in memory after a request, so the next video skips reloading it
OLLAMA_KEEP_ALIVE = os.environ.get("OLLAMA_KEEP_ALIVE", "30m")
FONT_DIR = HERE / "fonts"            # put NotoSansLao-Bold.ttf here
FONT_NAME = "Noto Sans Lao"
MAX_LINE_CHARS = 24                  # Lao chars per caption line before wrapping

LAO_RE = re.compile(r"[຀-໿]")
THAI_RE = re.compile(r"[฀-๿]")
LEADING_VOWELS = set("ເແໂໃໄ")
FOLLOWING_VOWELS = set("ະາຳຽໆ")


# ---------------------------------------------------------------- helpers
def run(cmd, **kw):
    subprocess.run(cmd, check=True, **kw)


def load_glossary():
    p = HERE / "glossary.txt"
    if not p.exists():
        return []
    return [l.strip() for l in p.read_text(encoding="utf-8").splitlines()
            if l.strip() and not l.startswith("#")]


def load_examples():
    p = HERE / "examples.jsonl"
    if not p.exists():
        return []
    return [json.loads(l) for l in p.read_text(encoding="utf-8").splitlines() if l.strip()]


def extract_audio(video: Path, wav: Path):
    # high-pass removes rumble, afftdn light denoise, loudnorm evens out volume
    run(["ffmpeg", "-y", "-loglevel", "error", "-i", str(video),
         "-af", "highpass=f=80,afftdn=nf=-25,loudnorm=I=-16:TP=-1.5:LRA=11",
         "-ar", str(SR), "-ac", "1", str(wav)])


def ts_srt(t):
    ms = int(round(t * 1000))
    h, ms = divmod(ms, 3600000)
    m, ms = divmod(ms, 60000)
    s, ms = divmod(ms, 1000)
    return f"{h:02}:{m:02}:{s:02},{ms:03}"


def ts_ass(t):
    cs = int(round(t * 100))
    h, cs = divmod(cs, 360000)
    m, cs = divmod(cs, 6000)
    s, cs = divmod(cs, 100)
    return f"{h}:{m:02}:{s:02}.{cs:02}"


def wrap_lao(text, width=MAX_LINE_CHARS):
    """Break a caption into lines of at most `width` chars without splitting a Lao syllable.
    Splits near the middle, then splits each half again until every line fits."""
    text = unicodedata.normalize("NFC", text.strip())
    if len(text) <= width:
        return [text]
    parts = _split_half(text)
    if len(parts) == 1:
        return parts
    return [line for p in parts for line in wrap_lao(p, width)]


def _split_half(text):
    mid = len(text) // 2
    # prefer an existing space close to the middle
    spaces = [i for i, c in enumerate(text) if c == " "]
    if spaces:
        i = min(spaces, key=lambda x: abs(x - mid))
        if abs(i - mid) < len(text) * 0.3:
            return [text[:i].strip(), text[i:].strip()]
    # otherwise break where a syllable clearly starts
    starts = [i for i in range(2, len(text) - 1) if _syllable_start(text, i)]
    if starts:
        i = min(starts, key=lambda x: abs(x - mid))
        return [text[:i].strip(), text[i:].strip()]
    # fallback: never before a combining mark / following vowel, never after a leading vowel
    for d in range(0, mid):
        for i in (mid + d, mid - d):
            if 0 < i < len(text):
                if unicodedata.category(text[i]) == "Mn" or text[i] in FOLLOWING_VOWELS:
                    continue
                if text[i - 1] in LEADING_VOWELS:
                    continue
                return [text[:i], text[i:]]
    return [text]


def _is_lao_consonant(c):
    return "ກ" <= c <= "ຮ" or "ໜ" <= c <= "ໟ"


def _syllable_start(text, i):
    """True if a new Lao syllable surely starts at text[i] (no dictionary needed):
    a leading vowel (ເແໂໃໄ), or a consonant carrying its own vowel/tone mark."""
    c, prev = text[i], text[i - 1]
    if prev in LEADING_VOWELS or prev == "ຫ":  # ຫ only ever prefixes the next consonant
        return False
    if c in LEADING_VOWELS:
        return True
    if not _is_lao_consonant(c) or i + 1 >= len(text):
        return False
    if c in "ວລຣ" and _is_lao_consonant(prev):  # medial of a cluster like ກວ້າງ
        return False
    nxt = text[i + 1]
    return unicodedata.category(nxt) == "Mn" or nxt in "ະາຳຽ"


# ---------------------------------------------------------------- model cache
# Models stay loaded for the life of the process. A one-off CLI run exits afterwards anyway;
# `serve` mode (used by the API) keeps them between videos instead of reloading ~7 GB each time.
_MODELS = {}


def cached(key, load):
    if key not in _MODELS:
        _MODELS[key] = load()
    return _MODELS[key]


def faster_whisper_model(name):
    from faster_whisper import WhisperModel
    return cached(("fw", name), lambda: WhisperModel(name, device="cpu", compute_type="int8",
                                                     cpu_threads=os.cpu_count()))


# ---------------------------------------------------------------- 1+2: whisper
def whisper_segments(audio, glossary):
    model = faster_whisper_model(WHISPER_MODEL)
    # Whisper's Lao is weak; Thai mode gives better phonetics, LLM converts to Lao later.
    prompt = " ".join(glossary)[:200] or None
    segs, _ = model.transcribe(
        audio, language="th", beam_size=WHISPER_BEAM, temperature=0,
        initial_prompt=prompt, condition_on_previous_text=False,
        vad_filter=True, vad_parameters={"min_silence_duration_ms": 300},
    )
    out = []
    for s in segs:
        text = s.text.strip()
        if text:
            out.append({"start": round(s.start, 2), "end": round(s.end, 2), "whisper": text})
    return out


# ---------------------------------------------------------------- 2b: BuzzASR Lao
def lao_asr_fill(audio, segs, glossary):
    """Transcribe each timed caption with BuzzASR in Lao (it cannot place timestamps itself)."""
    if not LAO_ASR_MODEL:
        return
    if not Path(LAO_ASR_MODEL).exists():
        print(f"  (skipped: {LAO_ASR_MODEL} not found, run: python get_asr_model.py)")
        return
    model = faster_whisper_model(LAO_ASR_MODEL)
    prompt = " ".join(g for g in glossary if LAO_RE.search(g))[:200] or None
    for s in segs:
        a = audio[max(0, int((s["start"] - 0.1) * SR)): int((s["end"] + 0.1) * SR)]
        parts, _ = model.transcribe(
            a, language="lo", beam_size=5, temperature=0, without_timestamps=True,
            initial_prompt=prompt, condition_on_previous_text=False, vad_filter=False,
            no_repeat_ngram_size=3, repetition_penalty=1.2)  # the model card's settings
        s["buzz"] = unicodedata.normalize("NFC", " ".join(p.text.strip() for p in parts).strip())


# ---------------------------------------------------------------- 3: wav2vec2
def w2v_models():
    import torch
    from transformers import Wav2Vec2ForCTC, Wav2Vec2Processor
    torch.set_num_threads(os.cpu_count())
    return cached(("w2v", W2V_MODEL), lambda: (Wav2Vec2Processor.from_pretrained(W2V_MODEL),
                                                Wav2Vec2ForCTC.from_pretrained(W2V_MODEL).eval()))


def w2v_fill(audio, segs):
    import torch
    proc, model = w2v_models()
    for s in segs:
        a = audio[max(0, int((s["start"] - 0.05) * SR)): int((s["end"] + 0.05) * SR)]
        x = proc(a, sampling_rate=SR, return_tensors="pt")
        with torch.no_grad():
            ids = model(x.input_values).logits.argmax(-1)
        s["w2v"] = proc.batch_decode(ids)[0].strip()


# ---------------------------------------------------------------- 4: LLM correction
SYSTEM_PROMPT = """You are a native Lao (ພາສາລາວ) transcription editor.
For each caption you get two noisy speech-recognition outputs of the SAME audio:
- "whisper": often written in THAI script, good at overall phonetics
- "w2v": written in LAO script, good at Lao spelling but may drop or garble letters
Write what the speaker actually said, in correct Lao script.
Rules:
- Output Lao script only (digits allowed). Never output Thai letters.
- Keep the speaker's spoken/colloquial words (ເດີ້, ເນາະ, ແຮະ). Do not formalise, summarise, translate, add or remove content.
- Fix only misheard words, using the context of the whole video and the glossary.
- Keep every id. One output line per input caption.
Return JSON: {"captions": [{"id": 0, "lao": "..."}, ...]}"""


def llm_fix(segs, glossary, examples, context=""):
    user = {
        "context": context or "Short social-media video, casual Lao speech.",
        "glossary": glossary,
        "examples": examples[:20],
        "captions": [{"id": i, "whisper": s["whisper"], "w2v": s.get("w2v", ""),
                      **({"buzz": s["buzz"]} if s.get("buzz") else {})}
                     for i, s in enumerate(segs)],
    }
    system = SYSTEM_PROMPT
    if any(s.get("buzz") for s in segs):  # only when the optional BuzzASR model is switched on
        system += ('\nSome captions also have "buzz": a Lao-trained Whisper guess in LAO script. '
                   "It is often wrong on fast casual speech: use it only to break a tie.")
    r = requests.post(f"{OLLAMA_URL}/api/chat", timeout=3600, json={
        "model": OLLAMA_MODEL,
        "format": "json",
        "stream": False,
        "keep_alive": OLLAMA_KEEP_ALIVE,
        "options": {"temperature": 0, "num_ctx": 8192},
        "messages": [{"role": "system", "content": system},
                     {"role": "user", "content": json.dumps(user, ensure_ascii=False)}],
    })
    r.raise_for_status()
    data = json.loads(r.json()["message"]["content"])
    fixed = {int(c["id"]): c["lao"].strip() for c in data.get("captions", [])}
    for i, s in enumerate(segs):
        t = fixed.get(i, "")
        ok = bool(t) and bool(LAO_RE.search(t)) and not THAI_RE.search(t)
        s["lao"] = unicodedata.normalize("NFC", t) if ok else best_raw(s)
        s["llm_ok"] = bool(ok)


def best_raw(s):
    """Fallback without the LLM: the first guess that is in Lao script."""
    for k in ("w2v", "buzz", "whisper"):
        t = s.get(k, "")
        if LAO_RE.search(t) and not THAI_RE.search(t):
            return unicodedata.normalize("NFC", t)
    return s.get("w2v") or s["whisper"]


# ---------------------------------------------------------------- SRT / ASS
def write_srt(segs, path):
    # one line per caption: read_srt joins lines with a space, so wrapping here would put
    # spaces into the Lao text. Line breaks are added when burning (write_ass).
    with open(path, "w", encoding="utf-8") as f:
        for i, s in enumerate(segs, 1):
            f.write(f"{i}\n{ts_srt(s['start'])} --> {ts_srt(s['end'])}\n{s['lao'].strip()}\n\n")


def read_srt(path):
    blocks = re.split(r"\n\s*\n", Path(path).read_text(encoding="utf-8-sig").strip())
    segs = []
    for b in blocks:
        lines = b.strip().splitlines()
        if len(lines) < 3:
            continue
        a, b2 = [x.strip() for x in lines[1].split("-->")]
        to_s = lambda t: sum(float(x) * m for x, m in zip(t.replace(",", ".").split(":"), (3600, 60, 1)))
        segs.append({"start": to_s(a), "end": to_s(b2), "lao": " ".join(l.strip() for l in lines[2:])})
    return segs


DEFAULT_STYLE = {
    "size": 3.7,               # font size, % of video height
    "position": 13,            # gap from the bottom, % of video height
    "color": "#FFFFFF",        # text colour
    "outline_color": "#000000",
    "animation": "none",       # none | fade | pop | slide
    "anim_ms": 300,            # how long the animation takes
    "sfx_volume": 1.0,         # loudness of caption sound effects (0-2)
    "max_lines": 2,            # longer captions are shown as pages of this many lines
}
SFX_DIR = HERE / "sfx"         # pop.wav, whoosh.wav ... (made by make_sfx.py)
PLACEHOLDER_TEXT = "ຂໍ້ຄວາມໃໝ່"   # the web editor's "new text" for an added caption
ANIMATIONS = ("none", "fade", "pop", "slide")


def load_style(raw):
    """Merge a JSON style string over the defaults, clamping to sane ranges."""
    st = dict(DEFAULT_STYLE)
    if raw:
        st.update({k: v for k, v in json.loads(raw).items() if k in DEFAULT_STYLE and v is not None})
    clamp = lambda v, lo, hi: max(lo, min(hi, float(v)))
    st["size"] = clamp(st["size"], 1.5, 12)
    st["position"] = clamp(st["position"], 2, 60)
    st["anim_ms"] = int(clamp(st["anim_ms"], 50, 2000))
    st["sfx_volume"] = clamp(st["sfx_volume"], 0, 2)
    st["max_lines"] = int(clamp(st["max_lines"], 1, 6))
    for k in ("color", "outline_color"):
        if not re.fullmatch(r"#[0-9A-Fa-f]{6}", str(st[k])):
            st[k] = DEFAULT_STYLE[k]
    if st["animation"] not in ANIMATIONS:
        st["animation"] = "none"
    return st


def ass_color(hex_rgb):
    r, g, b = hex_rgb[1:3], hex_rgb[3:5], hex_rgb[5:7]
    return f"&H00{b}{g}{r}".upper()


def anim_tag(st, w, h, size, margin_v):
    ms, a = st["anim_ms"], st["animation"]
    if a == "fade":
        return rf"{{\fad({ms},{ms})}}"
    if a == "pop":  # grow past full size, then settle
        p = int(ms * 0.7)
        return (rf"{{\fad({ms // 2},{ms // 2})\fscx60\fscy60"
                rf"\t(0,{p},\fscx108\fscy108)\t({p},{ms},\fscx100\fscy100)}}")
    if a == "slide":  # rise up from just below the resting position
        x, y = w // 2, h - margin_v
        return rf"{{\move({x},{y + round(size * 1.2)},{x},{y},0,{ms})\fad({ms},{ms // 2})}}"
    return ""


def write_ass(segs, path, w, h, style=None):
    st = style or load_style(None)
    size = round(h * st["size"] / 100)
    margin_v = round(h * st["position"] / 100)
    # measured: Noto Sans Lao averages ~0.31 x the ASS size per code point; 0.4 leaves room.
    # keep lines readable (<= 42 chars)
    width = max(8, min(42, int(w * 0.9 / (size * 0.4))))
    tag = anim_tag(st, w, h, size, margin_v)
    fg, outline = ass_color(st["color"]), ass_color(st["outline_color"])
    head = f"""[Script Info]
ScriptType: v4.00+
PlayResX: {w}
PlayResY: {h}
WrapStyle: 2

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,{FONT_NAME},{size},{fg},{fg},{outline},&H80000000,-1,0,0,0,100,100,0,0,1,{max(2, size // 12)},1.5,2,24,24,{margin_v},1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
"""
    with open(path, "w", encoding="utf-8") as f:
        f.write(head)
        for s in segs:
            for start, end, lines in caption_pages(s, width, st["max_lines"]):
                f.write(f"Dialogue: 0,{ts_ass(start)},{ts_ass(end)},Default,,0,0,0,,"
                        + tag + r"\N".join(lines) + "\n")


def caption_pages(s, width, max_lines):
    """Split a caption that wraps to more than max_lines into pages shown one after another.
    Each page gets a share of the caption's time in proportion to its length.
    The web editor's preview (captionPages in index.html) does the same."""
    lines = wrap_lao(s["lao"], width)
    pages = [lines[i:i + max_lines] for i in range(0, len(lines), max_lines)] or [[""]]
    weights = [max(1, sum(len(l) for l in p)) for p in pages]
    total, t, out = sum(weights), s["start"], []
    for k, (p, wgt) in enumerate(zip(pages, weights)):
        end = s["end"] if k == len(pages) - 1 else t + (s["end"] - s["start"]) * wgt / total
        out.append((t, end, p))
        t = end
    return out


def video_size(video):
    out = subprocess.check_output(["ffprobe", "-v", "error", "-select_streams", "v:0",
                                   "-show_entries", "stream=width,height:stream_side_data=rotation",
                                   "-of", "json", str(video)])
    st = json.loads(out)["streams"][0]
    w, h = st["width"], st["height"]
    rot = next((abs(int(d.get("rotation", 0))) for d in st.get("side_data_list", [])), 0)
    return (h, w) if rot in (90, 270) else (w, h)


# ---------------------------------------------------------------- commands
def cmd_transcribe(video: Path, args):
    work = video.with_suffix(".work")
    work.mkdir(exist_ok=True)
    wav = work / "audio.wav"
    extract_audio(video, wav)
    audio, _ = sf.read(wav, dtype="float32")
    glossary = load_glossary()

    print("[1/3] Whisper ...")
    segs = whisper_segments(audio, glossary)
    print("[2/3] Lao wav2vec2 ...")
    lao_asr_fill(audio, segs, glossary)
    w2v_fill(audio, segs)
    if args.no_llm:
        for s in segs:
            s["lao"] = best_raw(s)
    else:
        print(f"[3/3] LLM correction ({OLLAMA_MODEL}) ...")
        llm_fix(segs, glossary, load_examples(), args.context)

    (work / "candidates.json").write_text(json.dumps(segs, ensure_ascii=False, indent=1), encoding="utf-8")
    srt = video.with_suffix(".lo.srt")
    write_srt(segs, srt)
    for s in segs:
        flag = "" if s.get("llm_ok", True) else "  <-- LLM failed, check"
        print(f"{s['start']:6.2f}  {s['lao']}{flag}")
    print(f"\nReview/edit: {srt}\nThen run:    python {Path(__file__).name} burn \"{video}\"")


def cmd_burn(video: Path, args):
    srt = video.with_suffix(".lo.srt")
    segs = read_srt(srt)
    load_caption_sfx(video, segs)
    w, h = video_size(video)
    work = video.with_suffix(".work")
    work.mkdir(exist_ok=True)
    ass = work / "captions.ass"
    style = load_style(args.style)
    write_ass(segs, ass, w, h, style)
    fonts = work / "fonts"
    if not fonts.exists():
        shutil.copytree(FONT_DIR, fonts)
    out = video.with_name(video.stem + ".captioned.mp4")
    inputs, audio_filter, audio_map = sfx_mix(video, segs, style)
    # run inside work dir so the filter uses relative paths (avoids Windows "C:" escaping issues)
    if audio_filter:
        run(["ffmpeg", "-y", "-loglevel", "error", "-i", str(video.resolve()), *inputs,
             "-filter_complex", "[0:v]ass=captions.ass:fontsdir=fonts[v];" + audio_filter,
             "-map", "[v]", "-map", audio_map,
             "-c:v", "libx264", "-crf", "20", "-preset", "medium", "-pix_fmt", "yuv420p",
             "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", str(out.resolve())], cwd=work)
    else:
        run(["ffmpeg", "-y", "-loglevel", "error", "-i", str(video.resolve()),
             "-vf", "ass=captions.ass:fontsdir=fonts",
             "-c:v", "libx264", "-crf", "20", "-preset", "medium", "-pix_fmt", "yuv420p",
             "-c:a", "copy", "-movflags", "+faststart", str(out.resolve())], cwd=work)
    n = sum(1 for s in segs if s.get("sfx"))
    print(f"Done: {out}" + (f" ({n} sound effects)" if n else ""))


def load_caption_sfx(video: Path, segs):
    """Copy each caption's sound effect from <video>.captions.json (written by the web editor).
    Only lines whose timing still matches the .srt get one, so a hand-edited .srt stays safe."""
    side = video.with_suffix(".captions.json")
    if not side.exists():
        return
    saved = json.loads(side.read_text(encoding="utf-8")).get("segments", [])
    for s, c in zip(segs, saved):
        if abs(s["start"] - c.get("start", -1)) < 0.01 and abs(s["end"] - c.get("end", -1)) < 0.01:
            name = c.get("sfx") or ""
            if (SFX_DIR / f"{name}.wav").exists():
                s["sfx"] = name


def has_audio(video: Path):
    out = subprocess.check_output(["ffprobe", "-v", "error", "-select_streams", "a",
                                   "-show_entries", "stream=index", "-of", "csv=p=0", str(video)])
    return bool(out.strip())


def sfx_mix(video: Path, segs, style):
    """ffmpeg inputs + audio filter that lays each caption's sound effect over the video's audio
    at the caption's start time. Returns ([], "", "") when there is nothing to mix."""
    hits = [(s["start"], s["sfx"]) for s in segs if s.get("sfx")]
    if not hits or style["sfx_volume"] <= 0:
        return [], "", ""
    names = sorted({n for _, n in hits})
    inputs = [x for n in names for x in ("-i", str((SFX_DIR / f"{n}.wav").resolve()))]
    fmt = "aformat=sample_rates=48000:channel_layouts=stereo"
    parts, mixed = [], []
    for k, name in enumerate(names, start=1):
        times = [t for t, n in hits if n == name]
        outs = "".join(f"[s{k}_{j}]" for j in range(len(times)))
        parts.append(f"[{k}:a]{fmt},volume={style['sfx_volume']:.2f},asplit={len(times)}{outs}")
        for j, t in enumerate(times):
            ms = max(0, round(t * 1000))
            parts.append(f"[s{k}_{j}]adelay={ms}:all=1[d{k}_{j}]")
            mixed.append(f"[d{k}_{j}]")
    if has_audio(video):
        base = f"[0:a]{fmt}[base]"
    else:  # silent video: mix the effects over silence as long as the video
        dur = subprocess.check_output(["ffprobe", "-v", "error", "-show_entries", "format=duration",
                                       "-of", "csv=p=0", str(video)]).decode().strip()
        inputs += ["-f", "lavfi", "-t", dur, "-i", "anullsrc=r=48000:cl=stereo"]
        base = f"[{len(names) + 1}:a]{fmt}[base]"
    parts.append(base)
    parts.append(f"[base]{''.join(mixed)}amix=inputs={len(mixed) + 1}:normalize=0:duration=first:"
                 "dropout_transition=0,alimiter=limit=0.95[aout]")
    return inputs, ";".join(parts), "[aout]"


def cmd_collect(video: Path, args):
    """Save each corrected caption as a clip + text -> training data, and add LLM few-shot examples."""
    srt = video.with_suffix(".lo.srt")
    work = video.with_suffix(".work")
    segs = read_srt(srt)
    audio, _ = sf.read(work / "audio.wav", dtype="float32")
    cand = json.loads((work / "candidates.json").read_text(encoding="utf-8"))
    ds = HERE / "dataset"
    (ds / "clips").mkdir(parents=True, exist_ok=True)
    meta = ds / "metadata.csv"
    new = not meta.exists()
    with open(meta, "a", newline="", encoding="utf-8") as f:
        wr = csv.writer(f)
        if new:
            wr.writerow(["file_name", "text", "source"])
        for i, s in enumerate(segs):
            if s["lao"].strip() == PLACEHOLDER_TEXT:  # a caption added in the editor but never typed
                continue
            name = f"{video.stem}_{i:03}.wav"
            clip = audio[int(s["start"] * SR): int(s["end"] * SR)]
            sf.write(ds / "clips" / name, clip, SR)
            wr.writerow([f"clips/{name}", unicodedata.normalize("NFC", s["lao"]), video.name])
    # corrected lines become examples for the LLM next time (same timing -> same index)
    with open(HERE / "examples.jsonl", "a", encoding="utf-8") as f:
        # pair each caption with the machine guess it overlaps most in time: the editor can
        # add, split or delete captions, so matching by position would pair the wrong lines
        for s in segs:
            if s["lao"].strip() == PLACEHOLDER_TEXT:
                continue
            overlap = lambda c: min(s["end"], c["end"]) - max(s["start"], c["start"])
            c = max(cand, key=overlap, default=None)
            if c is None or overlap(c) < 0.5 * (s["end"] - s["start"]):
                continue  # no guess for this stretch of audio (e.g. a caption you added)
            if c.get("lao") != s["lao"]:
                f.write(json.dumps({"whisper": c["whisper"], "w2v": c.get("w2v", ""),
                                    "correct_lao": s["lao"]}, ensure_ascii=False) + "\n")
    print(f"Added {len(segs)} clips to {ds}")


def serve():
    """Run jobs from stdin, one JSON object per line, keeping models loaded between them:
      {"command": "transcribe"|"burn"|"collect", "video": "...", "no_llm": false, "context": "", "style": ""}
    After each job one line is printed: "@@DONE" or "@@ERROR <message>". Used by the Go API."""
    import traceback
    print("@@READY", flush=True)
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            req = json.loads(line)
            args = argparse.Namespace(no_llm=bool(req.get("no_llm")), context=req.get("context", ""),
                                      style=req.get("style", ""))
            video = Path(req["video"])
            if not video.exists():
                raise FileNotFoundError(f"Not found: {video}")
            {"transcribe": cmd_transcribe, "burn": cmd_burn, "collect": cmd_collect}[req["command"]](video, args)
            print("@@DONE", flush=True)
        except (Exception, SystemExit) as e:
            traceback.print_exc(file=sys.stdout)
            msg = str(e).replace("\n", " ") or e.__class__.__name__
            print(f"@@ERROR {msg}", flush=True)


def main():
    if sys.argv[1:2] == ["serve"]:
        return serve()
    ap = argparse.ArgumentParser(description="Lao auto-captions")
    ap.add_argument("command", choices=["transcribe", "burn", "collect", "auto", "serve"])
    ap.add_argument("video", type=Path)
    ap.add_argument("--no-llm", action="store_true", help="skip the LLM step")
    ap.add_argument("--context", default="", help="what the video is about, helps the LLM")
    ap.add_argument("--style", default="",
                    help='caption look as JSON, e.g. \'{"size":5,"animation":"pop"}\' '
                         "(keys: size, position, color, outline_color, animation, anim_ms)")
    args = ap.parse_args()
    if not args.video.exists():
        sys.exit(f"Not found: {args.video}")
    if args.command in ("transcribe", "auto"):
        cmd_transcribe(args.video, args)
    if args.command in ("burn", "auto"):
        cmd_burn(args.video, args)
    if args.command == "collect":
        cmd_collect(args.video, args)


if __name__ == "__main__":
    main()
