#include "creature.h"

// ---------- drawing helpers ----------

void thickLine(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w, uint16_t col) {
  bool steep = abs(y1 - y0) > abs(x1 - x0);
  for (int o = -w / 2; o < w - w / 2; o++) {
    if (steep) c->drawLine(x0 + o, y0, x1 + o, y1, col);
    else       c->drawLine(x0, y0 + o, x1, y1 + o, col);
  }
}

void plusSign(GFXcanvas16 *c, int x, int y, int s, uint16_t col) {
  if (s < 1) return;
  c->fillRect(x - s, y - 1, 2 * s, 3, col);
  c->fillRect(x - 1, y - s, 3, 2 * s, col);
}

void fillEllipse(GFXcanvas16 *c, int cx, int cy, int rx, int ry, uint16_t col) {
  if (rx < 1 || ry < 1) return;
  for (int dy = -ry; dy <= ry; dy++) {
    int dx = (int)(rx * sqrtf(1.0f - (float)(dy * dy) / (ry * ry)) + 0.5f);
    c->drawFastHLine(cx - dx, cy + dy, 2 * dx + 1, col);
  }
}

void arc(GFXcanvas16 *c, int cx, int cy, int r, float from, float to, int thick, uint16_t col) {
  for (float a = from; a <= to; a += 0.6f / max(r, 4)) {   // fine enough steps to leave no gaps
    int x = cx + (int)(r * cosf(a * PI)), y = cy + (int)(r * sinf(a * PI));
    if (thick <= 1) c->drawPixel(x, y, col);
    else c->fillCircle(x, y, thick / 2, col);
  }
}

void limb(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w, int out, uint16_t fill, uint16_t ink) {
  thickLine(c, x0, y0, x1, y1, w + 2 * out, ink);
  c->fillCircle(x1, y1, (w + 2 * out) / 2, ink);
  thickLine(c, x0, y0, x1, y1, w, fill);
  c->fillCircle(x1, y1, w / 2, fill);
}

// ---------- Famyy ----------

static const int WALL_W = 100, WALL_H = 86, ARM_REACH = 30;

int famyyHandX(const Look &k) { return k.cx + WALL_W / 2 + 8 + (int)(k.reach * ARM_REACH); }

// how far the whole house bounces, breathes or sinks this frame
static int bobOf(const Look &k, uint32_t t) {
  bool holding = k.reach > 0.02f;
  return (k.face == HAPPY && !holding) ? -(int)(fabsf(sinf(t / 260.0f)) * 8)
       : (k.face == ALARM)             ? -(int)(fabsf(sinf(t / 150.0f)) * 5)
       : (k.face == SLEEPING)          ? 2
       : (int)(sinf(t / 700.0f) * 1.5f);   // breathing
}

void famyyRoofTop(const Look &k, uint32_t t, int *x, int *y) {
  *x = k.cx;
  *y = GROUND_Y - WALL_H - 10 + bobOf(k, t) - 50;
}

static void eyesOval(GFXcanvas16 *c, int x, int y, int rx, int ry, const Palette &p) {   // big ovals with a shine
  for (int e : {x - 19, x + 19}) {
    fillEllipse(c, e, y, rx, ry, p.ink);
    if (ry > 6) c->fillCircle(e + rx / 3, y - ry / 2, max(1, rx / 3), 0xFFFF);
  }
}

void drawFamyy(GFXcanvas16 *c, uint32_t t, const Look &k, const Palette &p) {
  const Face f = k.face;
  const int O = OUTLINE;
  bool holding = k.reach > 0.02f;
  bool cheer = (f == HAPPY || f == ALARM) && !holding;   // arms up
  int bob = bobOf(k, t);
  int cx = k.cx + (f == ALARM ? (int)(sinf(t / 40.0f) * 2) : 0);
  int top = GROUND_Y - WALL_H - 10 + bob;   // the walls' top edge (feet end on the ground)
  int mid = top + 48;                           // shoulder height

  // smoke from the chimney while it thinks, or Fo is out working
  if (f == THINKING || f == WORKING) {
    for (int i = 0; i < 3; i++) {
      float ph = fmodf(t / 1800.0f + i / 3.0f, 1.0f);
      fillEllipse(c, cx + 34 + (int)(ph * 12), top - 46 - (int)(ph * 26), 4 + (int)(ph * 6), 3 + (int)(ph * 4), ph < 0.5f ? p.dim : p.soft);
    }
  }
  // the chimney, behind the roof
  c->fillRect(cx + 26 - O, top - 42 - O, 16 + 2 * O, 34, p.ink);
  c->fillRect(cx + 26, top - 42, 16, 34, p.roof);

  // feet, then arms
  limb(c, cx - 22, top + WALL_H - 4, cx - 24, top + WALL_H + 4, 6, O, p.wall, p.ink);
  limb(c, cx + 22, top + WALL_H - 4, cx + 24, top + WALL_H + 4, 6, O, p.wall, p.ink);
  if (cheer) limb(c, cx - WALL_W / 2, mid - 6, cx - WALL_W / 2 - 14, mid - 24, 6, O, p.wall, p.ink);
  else       limb(c, cx - WALL_W / 2, mid, cx - WALL_W / 2 - 10, mid + 14, 6, O, p.wall, p.ink);
  if (holding) {
    int hx = famyyHandX(k), hy = CREATURE_HAND_Y + bob + k.handDy;
    limb(c, cx + WALL_W / 2 - 2, mid - 2, hx, hy, 6, O, p.wall, p.ink);
    c->fillCircle(hx, hy, 7, p.ink);
    c->fillCircle(hx, hy, 4, p.wall);
  } else if (cheer) {
    limb(c, cx + WALL_W / 2, mid - 6, cx + WALL_W / 2 + 14, mid - 24, 6, O, p.wall, p.ink);
  } else {
    limb(c, cx + WALL_W / 2, mid, cx + WALL_W / 2 + 10, mid + 14, 6, O, p.wall, p.ink);
  }

  // walls, roof (with its brim), and the front door
  c->fillRoundRect(cx - WALL_W / 2 - O, top - O, WALL_W + 2 * O, WALL_H + 2 * O, 16 + O, p.ink);
  c->fillRoundRect(cx - WALL_W / 2, top, WALL_W, WALL_H, 16, p.wall);
  c->fillTriangle(cx - 66, top + 12, cx + 66, top + 12, cx, top - 50, p.ink);
  c->fillTriangle(cx - 58, top + 8, cx + 58, top + 8, cx, top - 43, p.roof);
  c->fillRoundRect(cx - 54, top + 6, 108, 7, 3, p.ink);
  if (k.nameFont) {   // its name, painted on the roof like a nameplate on a home
    const char *name = "Famyy";
    int w = 0;
    for (const char *ch = name; *ch; ch++) w += k.nameFont->glyph[*ch - k.nameFont->first].xAdvance;
    c->setFont(k.nameFont);
    c->setTextSize(1);
    c->setTextColor(p.sign);
    c->setCursor(cx - w / 2, top + 2);
    c->print(name);
    c->setFont(nullptr);
  }
  c->fillRoundRect(cx - 14, top + 60, 28, WALL_H - 58, 10, p.ink);
  c->fillRoundRect(cx - 12, top + 62, 24, WALL_H - 60, 9, p.door);
  c->fillCircle(cx + 6, top + 76, 2, p.ink);

  // the face
  int fx = cx + (int)(k.gazeX * 0.5f), fy = top + 34 + (int)(k.gazeY * 0.4f);
  int my = top + 47;   // mouth line
  bool talking = f == TALKING || k.talk > 0;
  if (f == SLEEPING) {
    for (int e : {fx - 19, fx + 19}) arc(c, e, fy - 4, 8, 0.2f, 0.8f, 3, p.ink);   // closed: two gentle curves
  } else if (f == HAPPY) {
    for (int e : {fx - 19, fx + 19}) arc(c, e, fy + 5, 9, 1.15f, 1.85f, 4, p.ink);   // ^ ^
  } else if (f == FOCUS) {
    for (int e : {fx - 19, fx + 19}) {
      fillEllipse(c, e, fy, 8, 11, p.ink);
      c->fillRect(e - 10, fy - 12, 21, 11, p.wall);   // heavy eyelids: concentrating
      c->fillRect(e - 10, fy - 2, 21, 3, p.ink);
    }
  } else if (f == ALARM) {
    eyesOval(c, fx, fy, 10, 14, p);
  } else {
    int ry = max(2, (int)(12 * k.blink * (f == LISTENING ? 1.1f : 1.0f)));
    eyesOval(c, fx, fy, 8, ry, p);
  }
  fillEllipse(c, cx - 33, top + 50, 7, 4, p.blush);
  fillEllipse(c, cx + 33, top + 50, 7, 4, p.blush);
  if (talking) {   // the mouth opens with the loudness of Famyy's own voice
    int open = constrain(k.talk / 450, 0, 7);
    fillEllipse(c, fx, my + 1, 7, 2 + open, p.ink);
    if (open > 2) fillEllipse(c, fx, my + 2 + open / 2, 4, open / 2, p.blush);
  } else if (f == HAPPY) {
    fillEllipse(c, fx, my - 1, 9, 7, p.ink);   // a big open smile: the top half is covered by the wall again
    c->fillRect(fx - 10, my - 9, 21, 8, p.wall);
    fillEllipse(c, fx, my + 3, 4, 2, p.blush);
  } else if (f == LISTENING || f == ASKING || f == ALARM) {
    fillEllipse(c, fx, my + 1, 4, f == ALARM ? 6 : 4, p.ink);   // "oh?"
  } else if (f == THINKING || f == WORKING) {
    thickLine(c, fx + 2, my + 1, fx + 11, my - 1, 3, p.ink);   // a sideways, pondering mouth
  } else if (f == FOCUS || f == SLEEPING) {
    c->fillRect(fx - 5, my, 10, 3, p.ink);
  } else {
    arc(c, fx, my - 6, 8, 0.2f, 0.8f, 3, p.ink);   // a gentle smile
  }

  // extras around it
  if (k.listen) {   // sound waves coming in from both sides
    for (int i = 0; i < 3; i++) {
      float ph = fmodf(t / 700.0f + i / 3.0f, 1.0f);
      int r = 6 + (int)((1 - ph) * 18);
      uint16_t col = ph > 0.66f ? p.ink : (ph > 0.33f ? p.dim : p.soft);
      arc(c, cx - WALL_W / 2 - 18, mid - 14, r, 0.72f, 1.28f, 2, col);
      arc(c, cx + WALL_W / 2 + 18, mid - 14, r, -0.28f, 0.28f, 2, col);
    }
  }
  if (f == ASKING) {
    c->setTextSize(3);
    c->setTextColor(p.ink);
    c->setCursor(cx + 70, top - 30);
    c->print('?');
  }
  if (f == HAPPY) {
    const int sp[4][2] = {{-92, -30}, {88, -20}, {84, 60}, {-86, 56}};
    for (int i = 0; i < 4; i++) plusSign(c, cx + sp[i][0], top + sp[i][1], (int)(max(0.0f, sinf(t / 280.0f + i * 2.1f)) * 7), p.accent);
  }
  if (f == ALARM && (t / 200) % 2) {
    for (int x : {cx - 92, cx + 88}) {
      c->fillRect(x, top - 10, 4, 16, p.ink);
      c->fillRect(x, top + 10, 4, 4, p.ink);
    }
  }
  if (f == SLEEPING) {
    for (int i = 0; i < 3; i++) {
      float ph = fmodf(t / 2600.0f + i / 3.0f, 1.0f);
      c->setTextSize(ph > 0.5f ? 2 : 1);
      c->setTextColor(ph < 0.33f ? p.ink : (ph < 0.66f ? p.dim : p.soft));
      c->setCursor(cx + 46 + (int)(ph * 20), top - 46 - (int)(ph * 30));
      c->print('z');
    }
  }
}
