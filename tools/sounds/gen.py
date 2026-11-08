#!/usr/bin/env python3
"""Synthesize Murmel's built-in event sounds.

Writes internal/audio/effects/sounds/<id>.ogg (48 kHz mono Vorbis via
ffmpeg). Pure synthesis, no samples: soft bell/marimba tones with short
attacks and exponential decays, peaking at about -10 dBFS.

Usage: tools/sounds/gen.py   (from the repository root)
"""
import math, os, struct, subprocess, tempfile, wave

RATE = 48000
OUT = "internal/audio/effects/sounds"
PEAK = 0.32  # ≈ -10 dBFS


def bell(buf, t0, freq, dur=0.45, partials=((1, 1.0, 1.0), (2.0, 0.28, 0.6), (3.01, 0.10, 0.4), (4.2, 0.05, 0.25)), tau=0.16, gain=1.0):
    """Add a struck tone: partials are (ratio, amplitude, decay factor)."""
    start = int(t0 * RATE)
    for i in range(int(dur * RATE)):
        t = i / RATE
        attack = min(1.0, t / 0.004)
        s = 0.0
        for ratio, amp, dk in partials:
            s += amp * math.sin(2 * math.pi * freq * ratio * t) * math.exp(-t / (tau * dk))
        j = start + i
        if j < len(buf):
            buf[j] += gain * attack * s


def glide(buf, t0, f0, f1, dur, gain=1.0):
    """Add a short sine glide with a smooth (raised-cosine) envelope."""
    start = int(t0 * RATE)
    n = int(dur * RATE)
    phase = 0.0
    for i in range(n):
        x = i / n
        f = f0 * (f1 / f0) ** x
        phase += 2 * math.pi * f / RATE
        env = math.sin(math.pi * x) ** 2
        j = start + i
        if j < len(buf):
            buf[j] += gain * env * (math.sin(phase) + 0.15 * math.sin(2 * phase))


def render(length):
    return [0.0] * int(length * RATE)


C5, E5, G5, A5, B5, C6, E6 = 523.25, 659.25, 783.99, 880.0, 987.77, 1046.5, 1318.5

SOUNDS = {}

b = render(0.75)
for k, f in enumerate((C5, E5, G5)):
    bell(b, 0.085 * k, f, gain=0.8)
SOUNDS["connect"] = b

b = render(0.75)
for k, f in enumerate((G5, E5, C5)):
    bell(b, 0.085 * k, f, gain=0.8)
SOUNDS["disconnect"] = b

b = render(0.6)
bell(b, 0.0, E5, tau=0.12)
bell(b, 0.1, B5, tau=0.14)
SOUNDS["join"] = b

b = render(0.6)
bell(b, 0.0, B5, tau=0.12)
bell(b, 0.1, E5, tau=0.14)
SOUNDS["leave"] = b

b = render(0.2)
glide(b, 0.0, 620, 930, 0.14)
SOUNDS["blip-up"] = b

b = render(0.2)
glide(b, 0.0, 930, 620, 0.14)
SOUNDS["blip-down"] = b

b = render(0.3)
glide(b, 0.0, 380, 760, 0.07, gain=0.9)
bell(b, 0.05, G5, dur=0.25, partials=((1, 1.0, 1.0), (4.0, 0.12, 0.2)), tau=0.05, gain=0.6)
SOUNDS["move"] = b

b = render(0.9)
bell(b, 0.0, A5, dur=0.9, tau=0.28)
bell(b, 0.0, E6, dur=0.9, tau=0.18, gain=0.25)
SOUNDS["message"] = b

b = render(0.35)
bell(b, 0.0, C6, dur=0.35, partials=((1, 1.0, 1.0), (2.0, 0.2, 0.5)), tau=0.07)
SOUNDS["tick"] = b


def write(name, buf):
    peak = max(abs(s) for s in buf) or 1.0
    fade = int(0.02 * RATE)
    data = bytearray()
    for i, s in enumerate(buf):
        v = s / peak * PEAK
        if i >= len(buf) - fade:
            v *= (len(buf) - i) / fade
        data += struct.pack("<h", int(max(-1, min(1, v)) * 32767))
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        path = tmp.name
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(bytes(data))
    out = os.path.join(OUT, name + ".ogg")
    subprocess.run(["ffmpeg", "-loglevel", "error", "-y", "-i", path, "-c:a", "libvorbis", "-q:a", "5", "-map_metadata", "-1", out], check=True)
    os.unlink(path)
    print(out, os.path.getsize(out), "bytes")


os.makedirs(OUT, exist_ok=True)
for name, buf in SOUNDS.items():
    write(name, buf)
