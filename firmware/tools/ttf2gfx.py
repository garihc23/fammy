#!/usr/bin/env python3
"""Turn a TrueType font into an Adafruit GFX font header: 1-bit glyphs for printable ASCII.

The GFX library's own fontconvert needs the FreeType headers; this does the same with PIL.

    tools/ttf2gfx.py /usr/share/fonts/truetype/quicksand/Quicksand-Bold.ttf 17 QuicksandBold17 femi/QuicksandBold17.h

threshold (0-255, default 100) decides which edge pixels become ink: lower keeps thin strokes whole.
"""
import sys
from PIL import Image, ImageDraw, ImageFont


def convert(path, size, name, threshold=100):
    font = ImageFont.truetype(path, size)
    ascent, descent = font.getmetrics()
    bitmaps, glyphs = bytearray(), []
    for code in range(0x20, 0x7F):
        ch = chr(code)
        advance = round(font.getlength(ch))
        x0, y0, x1, y1 = font.getbbox(ch)   # from the pen position, y measured down from the ascender line
        w, h = x1 - x0, y1 - y0
        offset = len(bitmaps)
        if w <= 0 or h <= 0:   # a space
            glyphs.append((offset, 0, 0, advance, 0, 0, ch))
            continue
        img = Image.new("L", (w, h), 0)
        ImageDraw.Draw(img).text((-x0, -y0), ch, font=font, fill=255)
        px = img.load()
        bits = [1 if px[x, y] >= threshold else 0 for y in range(h) for x in range(w)]
        for i in range(0, len(bits), 8):   # GFX reads glyph bits most significant first, straight across rows
            chunk = bits[i:i + 8] + [0] * (8 - len(bits[i:i + 8]))
            bitmaps.append(sum(b << (7 - k) for k, b in enumerate(chunk)))
        glyphs.append((offset, w, h, advance, x0, y0 - ascent, ch))
    return bitmaps, glyphs, ascent + descent


def header(name, source, size, bitmaps, glyphs, y_advance):
    out = [f"// {name}: {source} at {size} px, made by tools/ttf2gfx.py",
           "#pragma once", "#include <Adafruit_GFX.h>", "",
           f"const uint8_t {name}Bitmaps[] PROGMEM = {{"]
    for i in range(0, len(bitmaps), 16):
        out.append("  " + ", ".join(f"0x{b:02X}" for b in bitmaps[i:i + 16]) + ",")
    out += ["};", "", f"const GFXglyph {name}Glyphs[] PROGMEM = {{"]
    for offset, w, h, adv, xo, yo, ch in glyphs:
        label = "' '" if ch == " " else ("'\\\\'" if ch == "\\" else f"'{ch}'")
        out.append(f"  {{{offset}, {w}, {h}, {adv}, {xo}, {yo}}},   // {label}")
    out += ["};", "",
            f"const GFXfont {name} PROGMEM = {{(uint8_t *){name}Bitmaps, (GFXglyph *){name}Glyphs, 0x20, 0x7E, {y_advance}}};", ""]
    return "\n".join(out)


if __name__ == "__main__":
    if len(sys.argv) < 5:
        sys.exit(__doc__)
    path, size, name, dest = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
    threshold = int(sys.argv[5]) if len(sys.argv) > 5 else 100
    bitmaps, glyphs, y_advance = convert(path, size, name, threshold)
    with open(dest, "w") as f:
        f.write(header(name, path.rsplit("/", 1)[-1], size, bitmaps, glyphs, y_advance))
    print(f"{dest}: {len(bitmaps)} bytes of glyphs, line height {y_advance}")
