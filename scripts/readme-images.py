#!/usr/bin/env python3
"""Renders the README images into docs/images.

The panel screenshots come from the board tests (TestShot in
internal/panel), drawn offscreen at 2x in both themes; this script cuts
their rounded corners, sets them on a gradient with a shadow and writes a
light and a dark version of each image. It needs Pillow.

    python3 scripts/readme-images.py
"""

import os
import subprocess
import sys
import tempfile

from PIL import Image, ImageDraw, ImageFilter, ImageFont

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "docs", "images")
FONT = "/System/Library/Fonts/SFNS.ttf"

# The corner radius of the panel window in a 2x screenshot.
RADIUS = 28

LANGS = [
    ("en", "English"), ("ru", "Russian"), ("de", "German"), ("fr", "French"),
    ("es", "Spanish"), ("ja", "Japanese"), ("zh-Hans", "Chinese (Simplified)"),
    ("ar", "Arabic, right to left"),
]

FEATURES = [
    ("suggestions", "First run: models that fit"),
    ("settings-network-en", "Network access with an API key"),
    ("picker-remove-dialog", "Delete with the space it frees"),
    ("llamacpp-ready", "llama.cpp as the engine"),
]

# Boards taller than the others, cut to this height (2x pixels).
CROP = {"settings-network-en": 1062}

# The hero: a model downloading, the server generating, the model search.
HERO = ["picker-downloading", "generating", "add-model-variants"]

BOARDS = HERO + ["main-"] + [b for b, _ in FEATURES]

THEMES = {
    "dark": {"stops": [(15, 17, 26), (31, 27, 52), (16, 38, 48)], "text": (232, 233, 238), "shadow": 150},
    "light": {"stops": [(246, 239, 233), (233, 234, 248), (226, 241, 240)], "text": (40, 42, 50), "shadow": 60},
}


def shoot(dir):
    env = dict(os.environ, LUMILO_SHOT_DIR=dir, LUMILO_SHOT_ONLY=",".join(BOARDS), CGO_ENABLED="0")
    subprocess.run(["go", "test", "./internal/panel", "-run", "TestShot$", "-count=1"],
                   cwd=ROOT, env=env, check=True)


def panel(dir, board, theme, scale):
    """The board's screenshot with transparent corners, scaled."""
    im = Image.open(os.path.join(dir, f"{board}-{theme}.png")).convert("RGBA")
    if board in CROP:
        im = im.crop((0, 0, im.width, CROP[board]))
    # Draw the mask at 4x for smooth corners.
    big = Image.new("L", (im.width * 4, im.height * 4), 0)
    ImageDraw.Draw(big).rounded_rectangle((0, 0, big.width - 1, big.height - 1), RADIUS * 4, fill=255)
    im.putalpha(big.resize(im.size, Image.LANCZOS))
    return im.resize((round(im.width * scale), round(im.height * scale)), Image.LANCZOS)


def gradient(w, h, stops):
    """A diagonal gradient through three colours."""
    small = Image.new("RGB", (64, 64))
    px = small.load()
    for y in range(64):
        for x in range(64):
            t = (x + y) / 126
            a, b, u = (stops[0], stops[1], t * 2) if t < 0.5 else (stops[1], stops[2], t * 2 - 1)
            px[x, y] = tuple(round(a[i] + (b[i] - a[i]) * u) for i in range(3))
    return small.resize((w, h), Image.BICUBIC).convert("RGBA")


def place(canvas, im, x, y, shadow):
    """Pastes im at x, y over a soft shadow."""
    pad = 80
    sh = Image.new("RGBA", (im.width + 2 * pad, im.height + 2 * pad), (0, 0, 0, 0))
    mask = Image.new("L", sh.size, 0)
    mask.paste(im.getchannel("A").point(lambda a: a * shadow // 255), (pad, pad + 16))
    sh.putalpha(mask.filter(ImageFilter.GaussianBlur(28)))
    canvas.alpha_composite(sh, (x - pad, y - pad))
    canvas.alpha_composite(im, (x, y))


def label(canvas, text, cx, y, color, size=28):
    font = ImageFont.truetype(FONT, size)
    d = ImageDraw.Draw(canvas)
    w = d.textlength(text, font=font)
    d.text((cx - w / 2, y), text, font=font, fill=color)


def save(canvas, name):
    path = os.path.join(OUT, name)
    canvas.convert("RGB").save(path, optimize=True)
    print(os.path.relpath(path, ROOT), f"{os.path.getsize(path) // 1024} KB")


def hero(dir, theme, name="hero", aspect=None):
    """Three panels side by side; aspect (width / height) widens the margins,
    as the social preview of the repository needs 2:1."""
    t = THEMES[theme]
    side, mid = 0.78, 0.9
    left, centre, right = (panel(dir, b, theme, s) for b, s in zip(HERO, (side, mid, side)))
    gap, margin = 56, 110
    w = left.width + centre.width + right.width + 2 * gap + 2 * margin
    h = max(centre.height, left.height + 70, right.height + 70) + 2 * margin
    x0 = margin
    if aspect:
        x0 += (round(h * aspect) - w) // 2
        w = round(h * aspect)
    c = gradient(w, h, t["stops"])
    place(c, left, x0, margin + 70, t["shadow"])
    place(c, right, x0 + left.width + centre.width + 2 * gap, margin + 70, t["shadow"])
    place(c, centre, x0 + left.width + gap, margin, t["shadow"])
    if aspect:
        c = c.resize((1280, 640), Image.LANCZOS)
    save(c, f"{name}-{theme}.png")


def grid(dir, theme, items, cols, scale, name):
    """Panels in a grid, each with a caption under it."""
    t = THEMES[theme]
    ims = [(panel(dir, b, theme, scale), text) for b, text in items]
    cw = max(im.width for im, _ in ims)
    rows = [ims[i:i + cols] for i in range(0, len(ims), cols)]
    gap, margin, cap = 56, 90, 70
    heights = [max(im.height for im, _ in r) for r in rows]
    w = cols * cw + (cols - 1) * gap + 2 * margin
    h = sum(heights) + len(rows) * cap + (len(rows) - 1) * gap + 2 * margin
    c = gradient(w, h, t["stops"])
    y = margin
    for r, rh in zip(rows, heights):
        for i, (im, text) in enumerate(r):
            x = margin + i * (cw + gap)
            place(c, im, x, y, t["shadow"])
            label(c, text, x + cw / 2, y + rh + 20, t["text"])
        y += rh + cap + gap
    save(c, f"{name}-{theme}.png")


def main():
    os.makedirs(OUT, exist_ok=True)
    with tempfile.TemporaryDirectory() as dir:
        shoot(dir)
        for theme in THEMES:
            hero(dir, theme)
            grid(dir, theme, [("main-" + code, name) for code, name in LANGS], 4, 0.55, "languages")
            grid(dir, theme, FEATURES, 4, 0.6, "features")
        hero(dir, "dark", "social", 2)


if __name__ == "__main__":
    sys.exit(main())
