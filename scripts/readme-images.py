#!/usr/bin/env python3
"""Renders the README images into docs/images.

The panel screenshots come from the board tests (TestShot in
internal/panel), drawn offscreen at 2x in both themes; this script cuts
their rounded corners, sets them on a gradient with a shadow and writes a
light and a dark version of each image. The demo GIF comes from TestDemo's
frames. It needs Pillow and ffmpeg.

    python3 scripts/readme-images.py
"""

import json
import os
import shutil
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

# Both engines take turns: the README's own boards (readme-* in
# internal/panel/readme_test.go) run Qwen3.8-27B under llama.cpp and Splash.
FEATURES = [
    ("suggestions", "First run: models that fit"),
    ("readme-splash-models", "Image input and speculative decoding"),
    ("readme-llama-network", "Network access with an API key"),
    ("readme-splash-remove", "Delete with the space it frees"),
]

# Boards taller than the others, cut to this height (2x pixels).
CROP = {"readme-llama-network": 1062}

# The hero: llama.cpp with a model downloading and generating, Splash
# looking up a model's files.
HERO = ["readme-llama-downloading", "readme-llama-generating", "readme-splash-variants"]

# The same panels in the light and the dark theme, side by side.
THEMED = [("readme-engines", "light", "Light"), ("readme-engines", "dark", "Dark")]

BOARDS = HERO + ["readme-lang-"] + [b for b, _ in FEATURES] + [b for b, _, _ in THEMED]

THEMES = {
    "dark": {"stops": [(15, 17, 26), (31, 27, 52), (16, 38, 48)], "text": (232, 233, 238), "shadow": 150},
    "light": {"stops": [(246, 239, 233), (233, 234, 248), (226, 241, 240)], "text": (40, 42, 50), "shadow": 60},
}


# The menu-bar icon in each state, with its caption; Splash and llama.cpp
# take turns.
TRAY = [
    ("generating", "Generating: live speed"), ("ready-llamacpp", "Ready: memory in use"),
    ("loading", "Loading"), ("released-llamacpp", "Unloaded when idle"),
    ("stopped", "Stopped"), ("error-llamacpp", "Failed to start"),
]


def shoot(dir):
    env = dict(os.environ, LUMILO_SHOT_DIR=dir, LUMILO_SHOT_ONLY=",".join(BOARDS),
               LUMILO_TRAY_DIR=os.path.join(dir, "tray"), CGO_ENABLED="0")
    subprocess.run(["go", "test", "./internal/panel", "-run", "TestShot$", "-count=1"],
                   cwd=ROOT, env=env, check=True)
    subprocess.run(["go", "test", "./internal/trayicon", "-run", "TestTrayShot$", "-count=1"],
                   cwd=ROOT, env=env, check=True)


def tray(dir, state, theme, scale):
    """The menu-bar icon, drawn at 4x, scaled."""
    im = Image.open(os.path.join(dir, "tray", f"{state}-{theme}.png")).convert("RGBA")
    return im.resize((round(im.width * scale), round(im.height * scale)), Image.LANCZOS)


# The menu bar: its tint over the wallpaper and its text colour.
BAR = {"dark": ((18, 18, 24, 150), (245, 245, 247)), "light": ((255, 255, 255, 140), (29, 29, 31))}


def menubar(canvas, dir, theme, h, icon_cx, scale):
    """Draws a menu bar across the top with Lumilo's icon open at icon_cx."""
    tint, ink = BAR[theme]
    bar = canvas.crop((0, 0, canvas.width, h)).filter(ImageFilter.GaussianBlur(20))
    bar.alpha_composite(Image.new("RGBA", bar.size, tint))
    canvas.paste(bar, (0, 0))
    d = ImageDraw.Draw(canvas)
    font = ImageFont.truetype(FONT, round(13 * 2 * scale))
    bold = ImageFont.truetype(FONT, round(13 * 2 * scale))
    bold.set_variation_by_name("Bold")
    x, y = 36 * scale, h / 2
    for i, word in enumerate(["Finder", "File", "Edit", "View", "Go", "Window", "Help"]):
        f = bold if i == 0 else font
        d.text((x, y), word, font=f, fill=ink, anchor="lm")
        x += d.textlength(word, font=f) + 40 * scale
    clock = "Wed 28 Oct  14:32"
    d.text((canvas.width - 36 * scale, y), clock, font=font, fill=ink, anchor="rm")
    icon = tray(dir, "generating-llamacpp", theme, scale / 2)
    # The icon is highlighted while its panel is open.
    hl = (icon_cx - icon.width / 2 - 10 * scale, 4 * scale, icon_cx + icon.width / 2 + 10 * scale, h - 4 * scale)
    over = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    ImageDraw.Draw(over).rounded_rectangle(hl, 10 * scale, fill=(255, 255, 255, 46) if theme == "dark" else (0, 0, 0, 26))
    canvas.alpha_composite(over)
    canvas.alpha_composite(icon, (round(icon_cx - icon.width / 2), round((h - icon.height) / 2)))


def states(dir, theme):
    """The menu-bar icon in each state on a strip of menu bar, captioned."""
    t = THEMES[theme]
    tint, _ = BAR[theme]
    icons = [(tray(dir, s, theme, 0.75), text) for s, text in TRAY]
    cw, gap, margin, bar_h = 300, 24, 70, 80
    w = len(icons) * cw + (len(icons) - 1) * gap + 2 * margin
    h = 2 * margin + bar_h + 70
    c = gradient(w, h, t["stops"])
    strip = Image.new("RGBA", (w - 2 * margin, bar_h), tint)
    m = Image.new("L", strip.size, 0)
    ImageDraw.Draw(m).rounded_rectangle((0, 0, strip.width - 1, bar_h - 1), 22, fill=255)
    c.paste(strip, (margin, margin), m)
    for i, (im, text) in enumerate(icons):
        cx = margin + i * (cw + gap) + cw // 2
        c.alpha_composite(im, (cx - im.width // 2, margin + (bar_h - im.height) // 2))
        label(c, text, cx, margin + bar_h + 22, t["text"], size=26)
    save(c, f"menubar-{theme}.png")


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
    gap, margin, bar = 56, 110, round(24 * 2 * mid)
    w = left.width + centre.width + right.width + 2 * gap + 2 * margin
    h = max(centre.height, left.height + 70, right.height + 70) + 2 * margin
    x0 = margin
    if aspect:
        x0 += (round(h * aspect) - w) // 2
        w = round(h * aspect)
    c = gradient(w, h, t["stops"])
    place(c, left, x0, margin + 70, t["shadow"])
    place(c, right, x0 + left.width + centre.width + 2 * gap, margin + 70, t["shadow"])
    # The centre panel hangs from its icon in the menu bar.
    cx = x0 + left.width + gap
    menubar(c, dir, theme, bar, cx + centre.width / 2, mid)
    place(c, centre, cx, bar + 12, t["shadow"])
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


def themes(dir):
    """A panel in each theme, each on its own half of the gradient."""
    ims = [(panel(dir, b, theme, 0.75), theme, text) for b, theme, text in THEMED]
    cw = max(im.width for im, _, _ in ims)
    ch = max(im.height for im, _, _ in ims)
    margin, cap = 110, 70
    half = cw + 2 * margin
    h = ch + cap + 2 * margin
    c = Image.new("RGBA", (2 * half, h))
    for i, (im, theme, text) in enumerate(ims):
        t = THEMES[theme]
        c.paste(gradient(half, h, t["stops"]), (i * half, 0))
        place(c, im, i * half + margin + (cw - im.width) // 2, margin, t["shadow"])
        label(c, text, i * half + half / 2, margin + ch + 20, t["text"])
    save(c, "themes.png")


def shoot_demo(dir):
    env = dict(os.environ, LUMILO_DEMO_DIR=dir, CGO_ENABLED="0")
    subprocess.run(["go", "test", "./internal/panel", "-run", "TestDemo$", "-count=1"],
                   cwd=ROOT, env=env, check=True)


def demo(dir, theme):
    """The demo: the panel hangs from its icon in the menu bar while Lumilo
    starts Splash and serves requests, the speed live in the icon. The
    frames come from TestDemo in internal/panel; ffmpeg makes the GIF."""
    t = THEMES[theme]
    frames = json.load(open(os.path.join(dir, f"frames-{theme}.json")))
    scale, bar, margin = 0.75, 52, 40
    pw = round(744 * scale)
    w = pw + 2 * margin + 120
    h = bar + 16 + round(1100 * scale) + margin
    base = gradient(w, h, t["stops"])
    px = (w - pw) // 2
    icon_cx = px + pw / 2
    tmp = os.path.join(dir, "gif-" + theme)
    os.makedirs(tmp, exist_ok=True)
    for i, f in enumerate(frames):
        c = base.copy()
        im = Image.open(os.path.join(dir, f["panel"])).convert("RGBA")
        big = Image.new("L", (im.width * 4, im.height * 4), 0)
        ImageDraw.Draw(big).rounded_rectangle((0, 0, big.width - 1, big.height - 1), RADIUS * 4, fill=255)
        im.putalpha(big.resize(im.size, Image.LANCZOS))
        im = im.resize((pw, round(im.height * scale)), Image.LANCZOS)
        place(c, im, px, bar + 10, t["shadow"])
        # The menu bar with Lumilo's icon, highlighted as its panel is open.
        tint, _ = BAR[theme]
        strip = c.crop((0, 0, w, bar)).filter(ImageFilter.GaussianBlur(16))
        strip.alpha_composite(Image.new("RGBA", strip.size, tint))
        c.paste(strip, (0, 0))
        icon = Image.open(os.path.join(dir, f["tray"])).convert("RGBA")
        # At the scale of the 2x panel: the menu bar as it is.
        over = Image.new("RGBA", c.size, (0, 0, 0, 0))
        ImageDraw.Draw(over).rounded_rectangle(
            (icon_cx - icon.width / 2 - 8, 5, icon_cx + icon.width / 2 + 8, bar - 5), 8,
            fill=(255, 255, 255, 46) if theme == "dark" else (0, 0, 0, 26))
        c.alpha_composite(over)
        c.alpha_composite(icon, (round(icon_cx - icon.width / 2), round((bar - icon.height) / 2)))
        if f["click"][0]:
            # The pointer moves in from the lower right to Start.
            x, y = px + f["click"][0] * scale, bar + 10 + f["click"][1] * scale
            u = min(1, i / 18)
            x, y = x + (1 - u) * 160, y + (1 - u) * 220
            pointer(c, x, y, f["press"])
        c.convert("RGB").save(os.path.join(tmp, f"{i:03d}.png"))
    out = os.path.join(OUT, f"demo-{theme}.gif")
    palette = os.path.join(tmp, "palette.png")
    src = ["-framerate", "20", "-i", os.path.join(tmp, "%03d.png")]
    subprocess.run(["ffmpeg", "-loglevel", "error", "-y", *src, "-vf", "palettegen=stats_mode=diff:max_colors=256",
                    palette], check=True)
    subprocess.run(["ffmpeg", "-loglevel", "error", "-y", *src, "-i", palette, "-lavfi",
                    "paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle", "-loop", "0", out], check=True)
    shutil.rmtree(tmp)
    print(os.path.relpath(out, ROOT), f"{os.path.getsize(out) // 1024} KB")


def pointer(canvas, x, y, pressed):
    """The macOS arrow pointer with its tip at x, y; a ring while pressed."""
    d = ImageDraw.Draw(canvas)
    if pressed:
        d.ellipse((x - 18, y - 18, x + 18, y + 18), outline=(255, 255, 255, 200), width=3)
    s = 1.3
    arrow = [(0, 0), (0, 17), (4, 13), (7, 20), (10, 19), (7, 12), (12, 12)]
    pts = [(x + a * s, y + b * s) for a, b in arrow]
    d.polygon(pts, fill=(0, 0, 0), outline=(255, 255, 255), width=2)


def main():
    os.makedirs(OUT, exist_ok=True)
    with tempfile.TemporaryDirectory() as dir:
        shoot(dir)
        for theme in THEMES:
            hero(dir, theme)
            states(dir, theme)
            grid(dir, theme, [("readme-lang-" + code, name) for code, name in LANGS], 4, 0.55, "languages")
            grid(dir, theme, FEATURES, 4, 0.6, "features")
        hero(dir, "dark", "social", 2)
        themes(dir)
    with tempfile.TemporaryDirectory() as dir:
        shoot_demo(dir)
        for theme in THEMES:
            demo(dir, theme)


if __name__ == "__main__":
    sys.exit(main())
