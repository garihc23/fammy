#include "bubble.h"

static const int BASELINE = 12;   // from a line's top to where FreeSans letters sit

int textWidth(const GFXfont *font, const char *s, int n) {
  if (!font) return 6 * n;
  int w = 0;
  for (int i = 0; i < n; i++) {
    uint8_t ch = s[i];
    if (ch >= font->first && ch <= font->last) w += font->glyph[ch - font->first].xAdvance;
  }
  return w;
}

int wrapText(const GFXfont *font, const char *text, int n, int width, TextLine *lines, int maxLines) {
  const char *p = text, *end = text + n;
  int count = 0;
  while (p < end && count < maxLines) {
    while (p < end && *p == ' ') p++;
    if (p >= end) break;
    const char *start = p, *fit = nullptr;
    for (const char *q = p; q < end;) {   // take whole words while they fit
      const char *w = q;
      while (w < end && *w != ' ') w++;
      if (textWidth(font, start, w - start) > width) break;
      fit = w;
      q = w;
      while (q < end && *q == ' ') q++;
    }
    if (!fit) {   // one word wider than the line: cut it
      int k = 1;
      while (start + k < end && textWidth(font, start, k + 1) <= width) k++;
      fit = start + k;
    }
    lines[count++] = {start, (int)(fit - start)};
    p = fit;
  }
  return count;
}

int wordsUpTo(const char *text, int chars) {
  int len = strlen(text);
  if (chars <= 0) return 0;
  if (chars >= len) return len;
  while (chars < len && text[chars] != ' ') chars++;
  return chars;
}

int firstWordsCount(const char *text) {
  int words = 0;
  for (const char *p = text; *p;) {
    while (*p == ' ') p++;
    if (!*p) break;
    words++;
    while (*p && *p != ' ') p++;
  }
  return words;
}

int firstWords(const char *text, int words) {
  int i = 0, len = strlen(text);
  for (int w = 0; w < words && i < len; w++) {
    while (i < len && text[i] == ' ') i++;
    while (i < len && text[i] != ' ') i++;
  }
  return i;
}

// ---------- shapes ----------

void speechBubble(GFXcanvas16 *c, int x, int y, int w, int h, int tx, int ty, const BubbleStyle &s) {
  const int r = 10;
  int ax, ay, bx, by;   // where the tail leaves the bubble: on the side that faces its tip
  if (ty > y + h) {
    int m = constrain(tx, x + r + 8, x + w - r - 8);
    ax = m - 7, ay = y + h - 3, bx = m + 7, by = y + h - 3;
  } else if (tx < x) {
    int m = constrain(ty, y + r + 6, y + h - r - 6);
    ax = x + 3, ay = m - 6, bx = x + 3, by = m + 6;
  } else if (tx > x + w) {
    int m = constrain(ty, y + r + 6, y + h - r - 6);
    ax = x + w - 3, ay = m - 6, bx = x + w - 3, by = m + 6;
  } else {
    int m = constrain(tx, x + r + 8, x + w - r - 8);
    ax = m - 7, ay = y + 3, bx = m + 7, by = y + 3;
  }
  c->fillRoundRect(x - 2, y - 2, w + 4, h + 4, r + 2, s.ink);
  const int d[4][2] = {{-2, 0}, {2, 0}, {0, -2}, {0, 2}};
  for (auto &o : d) c->fillTriangle(ax + o[0], ay + o[1], bx + o[0], by + o[1], tx + o[0], ty + o[1], s.ink);
  c->fillRoundRect(x, y, w, h, r, s.fill);
  int sx = tx + (ax + bx) / 2 > 2 * tx ? 1 : -1, sy = ty + (ay + by) / 2 > 2 * ty ? 1 : -1;   // pull the tip in a little
  c->fillTriangle(ax, ay, bx, by, tx + sx * 2, ty + sy * 2, s.fill);
}

void thoughtCloud(GFXcanvas16 *c, int x, int y, int w, int h, int trailX, int trailY, const BubbleStyle &s) {
  int across = max(2, w / 28), down = max(1, h / 30);
  for (int pass = 0; pass < 2; pass++) {   // the whole outline first, then all the insides: one smooth edge
    uint16_t col = pass ? s.fill : s.ink;
    int g = pass ? 0 : 2;
    c->fillRoundRect(x - g, y - g, w + 2 * g, h + 2 * g, 14 + g, col);
    for (int i = 0; i <= across; i++) {   // puffs along the top and bottom
      int bx = x + 10 + i * (w - 20) / across;
      c->fillCircle(bx, y + 3, 11 + g, col);
      c->fillCircle(bx + (i < across ? 7 : -7), y + h - 3, 11 + g, col);
    }
    for (int j = 0; j < down; j++) {      // and down the sides
      int by = y + h * (j + 1) / (down + 1);
      c->fillCircle(x + 3, by, 11 + g, col);
      c->fillCircle(x + w - 3, by, 11 + g, col);
    }
  }
  // a trail of little puffs down to whoever is thinking
  int sx = x + 18, sy = y + h + 8;
  const float at[3] = {0.15f, 0.5f, 0.85f};
  const int rad[3] = {6, 4, 3};
  for (int i = 0; i < 3; i++) {
    int px = sx + (int)((trailX - sx) * at[i]), py = sy + (int)((trailY - sy) * at[i]);
    c->fillCircle(px, py, rad[i] + 2, s.ink);
    c->fillCircle(px, py, rad[i], s.fill);
  }
}

// ---------- text in bubbles ----------

static const int MAX_WRAP = 32;

int bubbleHeight(const BubbleStyle &s, const char *text, int n, int w, int maxLines) {
  TextLine lines[MAX_WRAP];
  int count = wrapText(s.font, text, n, w - 2 * BUBBLE_PAD, lines, MAX_WRAP);
  if (count == 0) return 0;
  return 2 * BUBBLE_PAD + min(count, maxLines) * BUBBLE_LINE_H - 3;
}

void bubbleText(GFXcanvas16 *c, int x, int y, int w, const char *text, int n, int maxLines, const BubbleStyle &s) {
  TextLine lines[MAX_WRAP];
  int count = wrapText(s.font, text, n, w - 2 * BUBBLE_PAD, lines, MAX_WRAP);
  int first = max(0, count - maxLines);   // long text scrolls: keep the newest lines
  c->setFont(s.font);
  c->setTextSize(1);
  c->setTextColor(s.text);
  for (int i = first; i < count; i++) {
    char buf[64];
    snprintf(buf, sizeof buf, "%.*s", min(lines[i].len, (int)sizeof buf - 1), lines[i].start);
    c->setCursor(x + BUBBLE_PAD, y + BUBBLE_PAD + (s.font ? BASELINE : 0) + (i - first) * BUBBLE_LINE_H);
    c->print(buf);
  }
  c->setFont(nullptr);   // everything else uses the built-in font
}
