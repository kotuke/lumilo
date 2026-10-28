"""The background of the disk image Lumilo ships in: an arrow from the app
to the link to Applications. scripts/make-dmg.sh lays the window out with
the same sizes.

Run this file with Python (it needs Pillow) to draw background.tiff again.
"""

import os

HERE = os.path.dirname(os.path.abspath(__file__))

# The window and the icons, in points.
WIDTH, HEIGHT = 640, 400
ICON_SIZE = 128
APP_AT, APPS_AT = (170, 205), (470, 205)

def draw(scale):
    from PIL import Image, ImageDraw, ImageFilter, ImageFont

    s = scale
    w, h = WIDTH * s, HEIGHT * s
    # A light wash in the icon's colours: lavender to a warm white.
    img = Image.new("RGB", (w, h))
    top, bottom = (241, 240, 252), (252, 248, 242)
    px = ImageDraw.Draw(img)
    for y in range(h):
        t = y / (h - 1)
        px.line([(0, y), (w, y)], fill=tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)))
    # A soft glow behind each icon.
    glow = Image.new("L", (w, h), 0)
    g = ImageDraw.Draw(glow)
    for cx, cy in (APP_AT, APPS_AT):
        r = 96 * s
        g.ellipse((cx * s - r, cy * s - r, cx * s + r, cy * s + r), fill=70)
    glow = glow.filter(ImageFilter.GaussianBlur(40 * s))
    img = Image.composite(Image.new("RGB", (w, h), (226, 224, 250)), img, glow)

    d = ImageDraw.Draw(img)
    # The arrow, between the icons, in the deep blue of the icon's tile.
    ink = (61, 64, 140)
    y = APP_AT[1] * s
    x0, x1 = (APP_AT[0] + ICON_SIZE // 2 + 30) * s, (APPS_AT[0] - ICON_SIZE // 2 - 30) * s
    d.line([(x0, y), (x1 - 10 * s, y)], fill=ink, width=5 * s)
    d.polygon([(x1 + 4 * s, y), (x1 - 18 * s, y - 14 * s), (x1 - 18 * s, y + 14 * s)], fill=ink)

    font = ImageFont.truetype("/System/Library/Fonts/SFNS.ttf", 18 * s)
    font.set_variation_by_name("Semibold")
    text = "Drag Lumilo to Applications"
    tw = d.textlength(text, font=font)
    d.text(((w - tw) / 2, 52 * s), text, font=font, fill=ink)
    return img


if __name__ == "__main__":
    import subprocess
    import tempfile

    with tempfile.TemporaryDirectory() as tmp:
        one, two = os.path.join(tmp, "bg.png"), os.path.join(tmp, "bg@2x.png")
        draw(1).save(one, dpi=(72, 72))
        draw(2).save(two, dpi=(144, 144))
        out = os.path.join(HERE, "background.tiff")
        subprocess.run(["tiffutil", "-cathidpicheck", one, two, "-out", out], check=True)
        print(out)
