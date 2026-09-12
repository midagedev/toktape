#!/usr/bin/env python3
"""Rasterise an ANSI (SGR truecolor/256/16) terminal frame to PNG.

Lead's E2E viewing tool for TUI frames when no terminal is at hand.
Usage: tools/ansi2png.py frame.ansi out.png [--font path.ttf] [--cjk path.ttf] [--size 16]
Handles: 38/48;2;r;g;b, 38/48;5;n, 30-37/90-97, 1 (bold), 2 (dim), 0 (reset).
East-Asian wide runes take two cells (falls back to the CJK font).
"""
import argparse
import re
import sys
import unicodedata
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_FONT = ROOT / "assets/fonts/JetBrainsMono-Regular.ttf"
DEFAULT_BOLD = ROOT / "assets/fonts/JetBrainsMono-Bold.ttf"
DEFAULT_CJK = ROOT / "assets/fonts/D2Coding-Regular.ttf"
BG = (17, 17, 27)
FG = (229, 231, 235)

BASIC = [(0, 0, 0), (205, 49, 49), (13, 188, 121), (229, 229, 16), (36, 114, 200), (188, 63, 188), (17, 168, 205), (229, 229, 229),
         (102, 102, 102), (241, 76, 76), (35, 209, 139), (245, 245, 67), (59, 142, 234), (214, 112, 214), (41, 184, 219), (255, 255, 255)]


def xterm256(n):
    if n < 16:
        return BASIC[n]
    if n < 232:
        n -= 16
        r, g, b = n // 36, (n // 6) % 6, n % 6
        return tuple(0 if v == 0 else 55 + v * 40 for v in (r, g, b))
    v = 8 + (n - 232) * 10
    return (v, v, v)


SGR = re.compile(r"\x1b\[([0-9;]*)m")
OTHER = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")


def wide(ch):
    return unicodedata.east_asian_width(ch) in ("W", "F")


def parse(text):
    """Yield rows of (char, fg, bg, bold, dim) cells."""
    rows, row = [], []
    fg, bg, bold, dim = FG, None, False, False
    i = 0
    text = OTHER.sub(lambda m: m.group(0) if m.group(0).endswith("m") else "", text)
    while i < len(text):
        m = SGR.match(text, i)
        if m:
            params = [int(p) if p else 0 for p in m.group(1).split(";")] or [0]
            j = 0
            while j < len(params):
                p = params[j]
                if p == 0:
                    fg, bg, bold, dim = FG, None, False, False
                elif p == 1:
                    bold = True
                elif p == 2:
                    dim = True
                elif p == 22:
                    bold = dim = False
                elif p == 39:
                    fg = FG
                elif p == 49:
                    bg = None
                elif 30 <= p <= 37:
                    fg = BASIC[p - 30]
                elif 90 <= p <= 97:
                    fg = BASIC[p - 90 + 8]
                elif 40 <= p <= 47:
                    bg = BASIC[p - 40]
                elif 100 <= p <= 107:
                    bg = BASIC[p - 100 + 8]
                elif p in (38, 48) and j + 1 < len(params):
                    mode = params[j + 1]
                    if mode == 2 and j + 4 < len(params):
                        col = tuple(params[j + 2:j + 5])
                        j += 4
                    elif mode == 5 and j + 2 < len(params):
                        col = xterm256(params[j + 2])
                        j += 2
                    else:
                        col = FG
                    if p == 38:
                        fg = col
                    else:
                        bg = col
                j += 1
            i = m.end()
            continue
        ch = text[i]
        i += 1
        if ch == "\n":
            rows.append(row)
            row = []
            continue
        if ch == "\r":
            continue
        row.append((ch, fg, bg, bold, dim))
        if wide(ch):
            row.append((None, fg, bg, bold, dim))  # second cell of a wide rune
    if row:
        rows.append(row)
    return rows


def render(rows, font_path, bold_path, cjk_path, size, pad=24):
    font = ImageFont.truetype(str(font_path), size)
    bold = ImageFont.truetype(str(bold_path or font_path), size)
    cjk = ImageFont.truetype(str(cjk_path), size) if cjk_path and Path(cjk_path).exists() else font
    cw = font.getlength("M")
    ch = int(size * 1.35)
    cols = max((len(r) for r in rows), default=0)
    img = Image.new("RGB", (int(cols * cw) + pad * 2, ch * len(rows) + pad * 2), BG)
    d = ImageDraw.Draw(img)
    for y, r in enumerate(rows):
        for x, cell in enumerate(r):
            c, fg, bg, b, dim = cell
            px, py = pad + x * cw, pad + y * ch
            if bg:
                d.rectangle([px, py, px + cw * (2 if (c and wide(c)) else 1), py + ch], fill=bg)
            if c is None or c == " ":
                continue
            col = tuple(int(v * 0.6) for v in fg) if dim else fg
            f = cjk if (wide(c) or ord(c) > 0xFFFF or unicodedata.category(c) == "Lo") else (bold if b else font)
            d.text((px, py + (ch - size) / 2 - 1), c, font=f, fill=col)
    return img


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src")
    ap.add_argument("out")
    ap.add_argument("--font", default=str(DEFAULT_FONT))
    ap.add_argument("--bold", default=str(DEFAULT_BOLD))
    ap.add_argument("--cjk", default=str(DEFAULT_CJK))
    ap.add_argument("--size", type=int, default=16)
    a = ap.parse_args()
    rows = parse(Path(a.src).read_text(encoding="utf-8", errors="replace"))
    img = render(rows, a.font, a.bold, a.cjk, a.size)
    img.save(a.out)
    print(a.out, img.size)


if __name__ == "__main__":
    sys.exit(main())
