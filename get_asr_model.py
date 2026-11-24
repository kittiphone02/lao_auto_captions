"""
Downloads BuzzASR/lao (Whisper large-v3 fine-tuned for Lao, MIT license) and converts it to
faster-whisper / CTranslate2 int8 format in ./models/buzzasr-lao-ct2. Run once (setup.bat does it).

  python get_asr_model.py

Needs about 7 GB of free RAM while converting.
"""
import json
import shutil
import sys
import tempfile
from pathlib import Path

from huggingface_hub import snapshot_download

HERE = Path(__file__).resolve().parent
REPO = "BuzzASR/lao"
OUT = HERE / "models" / "buzzasr-lao-ct2"


def main():
    if (OUT / "model.bin").exists() and "--force" not in sys.argv:
        print(f"Already converted: {OUT}  (use --force to redo)")
        return
    src = Path(snapshot_download(REPO))
    if "--vocab-only" in sys.argv:  # just rewrite vocabulary.json / tokenizer.json
        write_vocab(src)
        print("Vocabulary rewritten.")
        return
    print(f"Converting {src} -> {OUT} (int8) ...")
    from ctranslate2.converters import TransformersConverter
    TransformersConverter(str(src), copy_files=["preprocessor_config.json"]).convert(
        str(OUT), quantization="int8", force=True)
    # The converter reads the language ids from generation_config.lang_to_id, but newer
    # transformers drops that field on load, leaving lang_ids empty, and faster-whisper then
    # treats the model as English-only. Copy them straight from the model's own file.
    gen = json.loads((src / "generation_config.json").read_text(encoding="utf-8"))
    cfg_path = OUT / "config.json"
    cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
    if not cfg.get("lang_ids"):
        cfg["lang_ids"] = sorted(gen.get("lang_to_id", {}).values())
        cfg_path.write_text(json.dumps(cfg), encoding="utf-8")
    if not cfg["lang_ids"]:
        sys.exit("No language ids found; the model would be treated as English-only.")
    write_vocab(src)
    print("Done.")


def write_vocab(src):
    """BuzzASR replaced Whisper's BPE vocabulary. Its vocab.json still holds old copies of the
    special tokens at low ids (<|startoftranscript|> = 1 ...), while the model uses the added
    tokens at 51865+ (<|startoftranscript|> = 51866, <|lo|> = 51944). Both CTranslate2
    (vocabulary.json) and faster-whisper (tokenizer.json) look special tokens up by name, so
    the old copies are renamed and the real ids written out explicitly."""
    vocab = json.loads((src / "vocab.json").read_text(encoding="utf-8"))
    added = json.loads((src / "added_tokens.json").read_text(encoding="utf-8"))
    size = max(max(vocab.values()), max(added.values())) + 1
    tokens = [None] * size
    for tok, i in vocab.items():
        tokens[i] = f"<|unused_{i}|>" if tok in added else tok
    for tok, i in added.items():
        tokens[i] = tok
    if None in tokens:
        sys.exit(f"Gap in the vocabulary at id {tokens.index(None)}")
    # CTranslate2 marks a Whisper model multilingual only if its vocabulary holds an empty-string
    # token (stock Whisper has one at 50256). Use one of the unused old slots for it.
    if "" not in tokens:
        tokens[next(i for i, t in enumerate(tokens) if t.startswith("<|unused_"))] = ""
    (OUT / "vocabulary.json").write_text(json.dumps(tokens, ensure_ascii=False), encoding="utf-8")

    # start from the transformers fast tokenizer (BPE merges, byte-level settings), then fix ids
    from transformers import WhisperTokenizerFast
    with tempfile.TemporaryDirectory() as tmp:
        WhisperTokenizerFast.from_pretrained(str(src)).save_pretrained(tmp)
        tj = json.loads((Path(tmp) / "tokenizer.json").read_text(encoding="utf-8"))
    n_bpe = max(vocab.values()) + 1
    tj["model"]["vocab"] = {(tokens[i] or f"<|unused_{i}|>"): i for i in range(n_bpe)}
    tj["added_tokens"] = [{"id": i, "content": tok, "single_word": False, "lstrip": False, "rstrip": False,
                           "normalized": False, "special": True} for tok, i in sorted(added.items(), key=lambda x: x[1])]
    tj["post_processor"] = None  # it referenced the old low ids; faster-whisper adds its own prompt tokens
    (OUT / "tokenizer.json").write_text(json.dumps(tj, ensure_ascii=False), encoding="utf-8")

    from tokenizers import Tokenizer
    t = Tokenizer.from_file(str(OUT / "tokenizer.json"))
    for name in ("<|endoftext|>", "<|startoftranscript|>", "<|lo|>", "<|transcribe|>", "<|notimestamps|>"):
        if t.token_to_id(name) != added[name]:
            sys.exit(f"Tokenizer check failed for {name}: {t.token_to_id(name)} != {added[name]}")


if __name__ == "__main__":
    main()
