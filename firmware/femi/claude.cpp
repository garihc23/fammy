#include "claude.h"

// the mascot on an 11 x 6 grid of 3-pixel cells: a wide body, two eyes, arms out, four little legs
static const int CELL = 4, COLS = 11;
static const char *SPRITE[6] = {
  "..XXXXXXX..",
  "..XEXXXEX..",
  "..XEXXXEX..",
  "XXXXXXXXXXX",
  "..XXXXXXX..",
  "..X.X.X.X..",
};

ClaudePose claudePose(uint32_t now, bool on, uint32_t since, uint32_t until, bool foHere) {
  ClaudePose p = {};
  if (on) {
    uint32_t t = now - since;
    p.shown = true;
    if (t < CLAUDE_DROP_MS) {   // falling onto the roof, faster and faster
      float u = 1 - (float)t / CLAUDE_DROP_MS;
      p.dy = -(int)(u * u * 70);
      return p;
    }
    t -= CLAUDE_DROP_MS;
    p.squash = t < 120;
    p.spark = true;
    p.dy = (int)(sinf(t / 300.0f) * 1.5f);
    p.lookX = foHere ? 2 : 1;    // towards Fo, or up at the thought cloud
    p.lookY = foHere ? 0 : -1;
    return p;
  }
  if (until == 0) return p;   // it hasn't been here
  uint32_t t = now - until;
  if (t >= CLAUDE_LEAVE_MS) return p;
  if (t < 300) {   // a happy hop, arms up
    p.shown = true;
    p.armsUp = true;
    p.dy = -(int)(sinf(PI * t / 300.0f) * 14);
  } else {         // and away it pops
    p.sparkle = 2 + (int)((t - 300) / 30);
  }
  return p;
}

static void spark(GFXcanvas16 *c, int x, int y, uint32_t now, uint16_t col) {   // Claude's spark: rays turning slowly
  float turn = now / 900.0f;
  for (int i = 0; i < 8; i++) {
    float a = turn + i * PI / 4;
    int len = (i % 2 ? 7 : 10) + (int)(sinf(now / 200.0f + i) * 1.5f);
    int ex = x + (int)(cosf(a) * len), ey = y + (int)(sinf(a) * len);
    for (int o = -1; o <= 1; o++) c->drawLine(x + o, y, ex + o, ey, col);   // three pixels wide
    c->fillCircle(ex, ey, 1, col);                                           // rounded tips
  }
  c->fillCircle(x, y, 3, col);
}

void drawClaude(GFXcanvas16 *c, uint32_t now, const ClaudePose &p, int x, int y, uint16_t body, uint16_t eyes) {
  if (p.sparkle > 0) {   // popping away
    for (int i = 0; i < 6; i++) {
      float a = i * PI / 3 + 0.3f;
      int r = p.sparkle * 2;
      c->fillRect(x + (int)(cosf(a) * r) - 1, y - 10 + (int)(sinf(a) * r) - 1, 3, 3, body);
    }
    return;
  }
  if (!p.shown) return;
  int ch = p.squash ? 2 : CELL;                    // just landed: a little squashed and wider
  int cw = CELL, wide = p.squash ? 2 : 0;
  int left = x - COLS * cw / 2 - wide, top = y + p.dy - 6 * ch;
  bool step = (now / 260) % 2;                     // the legs shuffle while it thinks
  for (int r = 0; r < 6; r++) {
    for (int col = 0; col < COLS; col++) {
      char k = SPRITE[r][col];
      if (k == '.') continue;
      if (r == 3 && (col < 2 || col > 8) && p.armsUp) continue;   // arms go up instead (below)
      if (r == 5 && p.spark && ((col == 2 || col == 6) == step)) continue;   // alternate pairs of legs
      int px = left + col * cw + (col > 5 ? 2 * wide : (col > 1 ? wide : 0));
      c->fillRect(px, top + r * ch, cw, ch, body);   // eye cells too: the eyes are drawn over them
    }
  }
  // eyes: dark slots that can look about
  for (int col : {3, 7}) {
    int px = left + col * cw + (col > 5 ? 2 * wide : wide) + p.lookX / 2;
    c->fillRect(px, top + ch + p.lookY, cw - 1, 2 * ch - 1, eyes);
  }
  if (p.armsUp) {
    c->fillRect(left + wide, top + 2 * ch, cw, ch, body);
    c->fillRect(left - cw + wide, top + ch, cw, ch, body);
    c->fillRect(left + 9 * cw + 2 * wide, top + 2 * ch, cw, ch, body);
    c->fillRect(left + 10 * cw + 2 * wide, top + ch, cw, ch, body);
  }
  if (p.spark) spark(c, x, top - 15, now, body);
}
