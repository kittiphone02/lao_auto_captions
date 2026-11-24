"""
Builds the caption sound effects in ./sfx with ffmpeg's tone and noise generators.
Everything is synthesized here, so the sounds are free to use anywhere.

  python make_sfx.py
"""
import re
import subprocess
from pathlib import Path

HERE = Path(__file__).resolve().parent
OUT = HERE / "sfx"
SR = 48000

# name -> (ffmpeg lavfi source, extra audio filter)
SOUNDS = {
    # short downward blip, like a bubble bursting
    "pop": ("aevalsrc=exprs='sin(2*PI*(380+1500*exp(-t*45))*t)*exp(-t*28)':d=0.16", ""),
    # tiny dry tick
    "click": ("aevalsrc=exprs='(random(0)*2-1)*exp(-t*350)+0.6*sin(2*PI*2200*t)*exp(-t*220)':d=0.04",
              "highpass=f=900"),
    # airy rise and fall of filtered noise
    "whoosh": ("anoisesrc=d=0.55:c=pink:a=0.9",
               "highpass=f=250,lowpass=f=4200,volume='sin(PI*t/0.55)^2':eval=frame"),
    # bright bell
    "ding": ("aevalsrc=exprs='(sin(2*PI*1318*t)+0.45*sin(2*PI*2637*t)+0.2*sin(2*PI*3951*t))*exp(-t*5)':d=1.0", ""),
    # quick upward chirp
    "bubble": ("aevalsrc=exprs='sin(2*PI*(260+2600*t)*t)*exp(-t*22)':d=0.14", ""),
    # fast high swish
    "swipe": ("anoisesrc=d=0.25:c=white:a=0.8",
              "highpass=f=1800,lowpass=f=9000,volume='sin(PI*t/0.25)^3':eval=frame"),
    # deep hit
    "boom": ("aevalsrc=exprs='sin(2*PI*(45+110*exp(-t*9))*t)*exp(-t*4.5)':d=0.9", "lowpass=f=900"),
}


def peak_db(path):
    out = subprocess.run(["ffmpeg", "-hide_banner", "-i", str(path), "-af", "volumedetect", "-f", "null", "-"],
                         capture_output=True, text=True).stderr
    return float(re.search(r"max_volume: (-?[\d.]+) dB", out).group(1))


def main():
    OUT.mkdir(exist_ok=True)
    for name, (src, af) in SOUNDS.items():
        raw, dst = OUT / f"{name}.raw.wav", OUT / f"{name}.wav"
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", src, "-af", af or "anull",
                        "-ar", str(SR), "-ac", "1", str(raw)], check=True)
        # normalise every sound to the same -6 dBFS peak so the volume slider behaves the same
        gain = -6 - peak_db(raw)
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-i", str(raw), "-af", f"volume={gain:.2f}dB",
                        "-ar", str(SR), "-ac", "1", "-c:a", "pcm_s16le", str(dst)], check=True)
        raw.unlink()
        print(f"{name:7} peak {peak_db(dst):6.1f} dB")


if __name__ == "__main__":
    main()
