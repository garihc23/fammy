// Design options for Famyy's new look, each beside Fo as Wajo draws it (white marshmallow, black line art).
// Not firmware: the chosen design moves into femi/creature.cpp.
//   g++ ... options.cpp -o options && ./options out.ppm
#include <Adafruit_GFX.h>
#include "../femi/creature.h"

static constexpr uint16_t rgb(uint8_t r, uint8_t g, uint8_t b) { return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3); }
static const uint16_t BG = rgb(255, 246, 232), INK = rgb(43, 36, 32), WHITE = rgb(255, 255, 255);
static const uint16_t BLUSH = rgb(255, 160, 160), SOFT = rgb(228, 214, 196);
static const int OUT = 3;   // outline thickness

static void fillEllipse(GFXcanvas16 *c, int cx, int cy, int rx, int ry, uint16_t col) {
  for (int dy = -ry; dy <= ry; dy++) {
    int dx = (int)(rx * sqrtf(1.0f - (float)(dy * dy) / (ry * ry)) + 0.5f);
    c->drawFastHLine(cx - dx, cy + dy, 2 * dx + 1, col);
  }
}
static void smile(GFXcanvas16 *c, int cx, int cy, int r, uint16_t col) {
  for (float a = 0.2f * PI; a <= 0.8f * PI; a += 0.05f) c->fillCircle(cx + (int)(r * cosf(a)), cy + (int)(r * sinf(a)), 1, col);
}
static void limb(GFXcanvas16 *c, int x0, int y0, int x1, int y1, uint16_t fill) {   // a stubby outlined arm or leg
  thickLine(c, x0, y0, x1, y1, 10, INK);
  c->fillCircle(x1, y1, 5, INK);
  thickLine(c, x0, y0, x1, y1, 4, fill);
  c->fillCircle(x1, y1, 2, fill);
}
static void eyes(GFXcanvas16 *c, int x, int y, int gap, int rx, int ry) {   // Fo-style tall ovals, with a glint
  for (int e : {x - gap / 2, x + gap / 2}) {
    fillEllipse(c, e, y, rx, ry, INK);
    c->fillCircle(e + rx / 3, y - ry / 2, max(1, rx / 3), WHITE);
  }
}

// ---------- Fo ----------
// Fo's hand, as Wajo draws it: a small palm with four fingers spread and a thumb, all outlined
static void foHand(GFXcanvas16 *c, int x, int y, float dir) {
  const float spread[5] = {-0.62f, -0.2f, 0.2f, 0.6f, 1.45f};   // four fingers around the arm's direction, then the thumb
  const int len[5] = {7, 9, 9, 7, 6};
  int ex[5], ey[5];
  for (int i = 0; i < 5; i++) {
    ex[i] = x + (int)(cosf(dir + spread[i]) * len[i]);
    ey[i] = y + (int)(sinf(dir + spread[i]) * len[i]);
    thickLine(c, x, y, ex[i], ey[i], 6, INK);
    c->fillCircle(ex[i], ey[i], 3, INK);
  }
  c->fillCircle(x, y, 6, INK);
  for (int i = 0; i < 5; i++) {
    thickLine(c, x, y, ex[i], ey[i], 2, WHITE);
    c->fillCircle(ex[i], ey[i], 1, WHITE);
  }
  c->fillCircle(x, y, 4, WHITE);
}

static void drawFo(GFXcanvas16 *c, int fx, bool wave) {
  // outlines first, then the white on top, so the joined shapes read as one body
  limb(c, fx - 11, 146, fx - 12, 160, WHITE);
  limb(c, fx + 11, 146, fx + 12, 160, WHITE);
  limb(c, fx - 27, 118, fx - 35, 134, WHITE);
  if (wave) {
    limb(c, fx + 27, 112, fx + 38, 94, WHITE);
    foHand(c, fx + 40, 89, atan2f(-18, 11));
  }
  else limb(c, fx + 27, 118, fx + 35, 134, WHITE);
  c->fillRoundRect(fx - 28 - OUT, 86 - OUT, 56 + 2 * OUT, 62 + 2 * OUT, 22 + OUT, INK);
  c->fillRoundRect(fx - 28, 86, 56, 62, 22, WHITE);
  fillEllipse(c, fx - 11, 110, 4, 7, INK);
  fillEllipse(c, fx + 3, 110, 4, 7, INK);
  smile(c, fx - 4, 117, 5, INK);
}


// ---------- Fo, closer to Wajo's drawings ----------
// thin black line art around white: a squircle body, big solid oval eyes set close, a tiny U smile,
// thin arms with mitten hands (an open hand to wave), short chunky legs
static const int FOUT = 2;
static void tube(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w) {   // a white tube with a thin outline
  thickLine(c, x0, y0, x1, y1, w + 2 * FOUT, INK);
  c->fillCircle(x1, y1, (w + 2 * FOUT) / 2, INK);
  c->fillCircle(x0, y0, (w + 2 * FOUT) / 2, INK);
  thickLine(c, x0, y0, x1, y1, w, WHITE);
  c->fillCircle(x1, y1, w / 2, WHITE);
}
static void blob(GFXcanvas16 *c, int cx, int cy, int rx, int ry) {
  fillEllipse(c, cx, cy, rx + FOUT, ry + FOUT, INK);
  fillEllipse(c, cx, cy, rx, ry, WHITE);
}
static void openHand(GFXcanvas16 *c, int x, int y, float dir) {   // palm, four fingers spread, thumb out: a wave
  const float spread[5] = {-0.56f, -0.19f, 0.19f, 0.56f, 1.45f};
  const int len[5] = {13, 15, 15, 13, 10};
  for (int pass = 0; pass < 2; pass++)
    for (int i = 0; i < 5; i++) {
      int ex = x + (int)(cosf(dir + spread[i]) * len[i]), ey = y + (int)(sinf(dir + spread[i]) * len[i]);
      if (pass == 0) { thickLine(c, x, y, ex, ey, 4, INK); c->fillCircle(ex, ey, 2, INK); }
      else { thickLine(c, x, y, ex, ey, 2, WHITE); c->drawPixel(ex, ey, WHITE); }
    }
  fillEllipse(c, x, y, 6, 6, INK);
  fillEllipse(c, x, y, 5, 5, WHITE);
}
enum FoArm { ARM_DOWN, ARM_WAVE, ARM_REACH, ARM_UP };
static void drawFo2(GFXcanvas16 *c, int fx, FoArm left, FoArm right, int look) {
  const int top = 86, w = 54, h = 60;
  // legs and feet first, so the body sits on them
  tube(c, fx - 9, top + h - 6, fx - 10, top + h + 9, 9);
  tube(c, fx + 9, top + h - 6, fx + 10, top + h + 9, 9);
  blob(c, fx - 13, top + h + 12, 7, 3);
  blob(c, fx + 13, top + h + 12, 7, 3);
  // arms
  int sy = top + 30;
  if (left == ARM_REACH) { tube(c, fx - w / 2 + 2, sy, fx - w / 2 - 14, sy - 4, 4); blob(c, fx - w / 2 - 17, sy - 5, 5, 4); }
  else if (left == ARM_UP) { tube(c, fx - w / 2 + 2, sy - 2, fx - w / 2 - 10, sy - 18, 4); blob(c, fx - w / 2 - 11, sy - 21, 4, 4); }
  else { tube(c, fx - w / 2 + 2, sy, fx - w / 2 - 6, sy + 14, 4); blob(c, fx - w / 2 - 6, sy + 17, 4, 4); }
  if (right == ARM_WAVE) { tube(c, fx + w / 2 - 2, sy - 2, fx + w / 2 + 9, sy - 18, 4); openHand(c, fx + w / 2 + 11, sy - 24, atan2f(-16, 3)); }
  else if (right == ARM_UP) { tube(c, fx + w / 2 - 2, sy - 2, fx + w / 2 + 10, sy - 18, 4); blob(c, fx + w / 2 + 11, sy - 21, 4, 4); }
  else { tube(c, fx + w / 2 - 2, sy, fx + w / 2 + 6, sy + 14, 4); blob(c, fx + w / 2 + 6, sy + 17, 4, 4); }
  // the body: a soft squircle
  c->fillRoundRect(fx - w / 2 - FOUT, top - FOUT, w + 2 * FOUT, h + 2 * FOUT, 22 + FOUT, INK);
  c->fillRoundRect(fx - w / 2, top, w, h, 22, WHITE);
  // face: two big solid ovals close together, a small U smile between and below them
  int ex = fx + look;
  fillEllipse(c, ex - 8, top + 22, 5, 8, INK);
  fillEllipse(c, ex + 8, top + 22, 5, 8, INK);
  for (float a = 0.12f * PI; a <= 0.88f * PI; a += 0.04f) c->fillCircle(ex + (int)(5 * cosf(a)), top + 33 + (int)(4 * sinf(a)), 1, INK);
}

// ---------- A: a little house with a face — Famyy is home ----------
static void famyyHouse(GFXcanvas16 *c, int cx, uint32_t t) {
  const uint16_t WALL = rgb(255, 178, 138), ROOF = rgb(214, 98, 74), DOOR = rgb(232, 140, 104);
  for (int i = 0; i < 3; i++) {   // smoke from the chimney
    float p = fmodf(t / 2400.0f + i / 3.0f, 1.0f);
    fillEllipse(c, cx + 34 + (int)(p * 10), 26 - (int)(p * 22), 4 + (int)(p * 5), 3 + (int)(p * 4), SOFT);
  }
  c->fillRect(cx + 26 - OUT, 28 - OUT, 16 + 2 * OUT, 34, INK);
  c->fillRect(cx + 26, 28, 16, 34, ROOF);
  limb(c, cx - 22, 152, cx - 24, 160, WALL);
  limb(c, cx + 22, 152, cx + 24, 160, WALL);
  limb(c, cx - 50, 118, cx - 60, 132, WALL);
  limb(c, cx + 50, 118, cx + 60, 132, WALL);
  c->fillRoundRect(cx - 50 - OUT, 70 - OUT, 100 + 2 * OUT, 86 + 2 * OUT, 16 + OUT, INK);
  c->fillRoundRect(cx - 50, 70, 100, 86, 16, WALL);
  c->fillTriangle(cx - 66, 82, cx + 66, 82, cx, 20, INK);
  c->fillTriangle(cx - 58, 78, cx + 58, 78, cx, 27, ROOF);
  c->fillRoundRect(cx - 54, 76, 108, 7, 3, INK);
  eyes(c, cx, 104, 38, 8, 12);
  fillEllipse(c, cx - 32, 120, 7, 4, BLUSH);
  fillEllipse(c, cx + 32, 120, 7, 4, BLUSH);
  smile(c, cx, 116, 8, INK);
  c->fillRoundRect(cx - 12 - 2, 132 - 2, 24 + 4, 24 + 2, 10, INK);
  c->fillRoundRect(cx - 12, 132, 24, 26, 9, DOOR);
  c->fillCircle(cx + 6, 146, 2, INK);
}

// ---------- B: a round, warm buddy with a sprout — grows with the family ----------
static void famyyBun(GFXcanvas16 *c, int cx, uint32_t t) {
  const uint16_t SKIN = rgb(255, 196, 150), BELLY = rgb(255, 226, 196), LEAF = rgb(112, 190, 110);
  float sway = sinf(t / 500.0f) * 4;
  thickLine(c, cx, 68, cx + (int)sway, 44, 5, INK);
  thickLine(c, cx, 68, cx + (int)sway, 44, 2, LEAF);
  fillEllipse(c, cx + (int)sway - 9, 40, 11, 6, INK);
  fillEllipse(c, cx + (int)sway - 9, 40, 8, 4, LEAF);
  fillEllipse(c, cx + (int)sway + 9, 36, 11, 6, INK);
  fillEllipse(c, cx + (int)sway + 9, 36, 8, 4, LEAF);
  limb(c, cx - 22, 148, cx - 24, 160, SKIN);
  limb(c, cx + 22, 148, cx + 24, 160, SKIN);
  limb(c, cx - 52, 114, cx - 62, 128, SKIN);
  limb(c, cx + 52, 114, cx + 62, 128, SKIN);
  fillEllipse(c, cx, 110, 56 + OUT, 46 + OUT, INK);
  fillEllipse(c, cx, 110, 56, 46, SKIN);
  fillEllipse(c, cx, 132, 30, 18, BELLY);
  eyes(c, cx, 100, 40, 9, 13);
  fillEllipse(c, cx - 34, 118, 8, 5, BLUSH);
  fillEllipse(c, cx + 34, 118, 8, 5, BLUSH);
  smile(c, cx, 112, 9, INK);
}

// ---------- C: today's robot, made cosy: soft colours, a heart antenna, a friendly screen ----------
static void famyyRobot(GFXcanvas16 *c, int cx, uint32_t t) {
  const uint16_t SHELL = rgb(146, 206, 196), SCREEN = rgb(38, 52, 60), GLOW = rgb(190, 255, 236), HEART = rgb(255, 110, 120);
  thickLine(c, cx, 62, cx, 44, 5, INK);
  thickLine(c, cx, 62, cx, 44, 2, SHELL);
  float beat = 1 + 0.15f * fabsf(sinf(t / 300.0f));
  int r = (int)(6 * beat);
  c->fillCircle(cx - r / 2 - 1, 38, r / 2 + 3, INK);
  c->fillCircle(cx + r / 2 + 1, 38, r / 2 + 3, INK);
  c->fillTriangle(cx - r - 3, 39, cx + r + 3, 39, cx, 39 + r + 6, INK);
  c->fillCircle(cx - r / 2 - 1, 38, r / 2 + 1, HEART);
  c->fillCircle(cx + r / 2 + 1, 38, r / 2 + 1, HEART);
  c->fillTriangle(cx - r - 1, 39, cx + r + 1, 39, cx, 39 + r + 3, HEART);
  limb(c, cx - 22, 150, cx - 24, 160, SHELL);
  limb(c, cx + 22, 150, cx + 24, 160, SHELL);
  limb(c, cx - 56, 112, cx - 66, 126, SHELL);
  limb(c, cx + 56, 112, cx + 66, 126, SHELL);
  c->fillRoundRect(cx - 56 - OUT, 62 - OUT, 112 + 2 * OUT, 90 + 2 * OUT, 26 + OUT, INK);
  c->fillRoundRect(cx - 56, 62, 112, 90, 26, SHELL);
  c->fillRoundRect(cx - 40, 76, 80, 56, 18, SCREEN);
  for (int e : {cx - 16, cx + 16}) {
    fillEllipse(c, e, 98, 7, 10, GLOW);
    c->fillCircle(e + 2, 94, 2, SCREEN);
  }
  fillEllipse(c, cx - 30, 116, 6, 3, BLUSH);
  fillEllipse(c, cx + 30, 116, 6, 3, BLUSH);
  smile(c, cx, 108, 7, GLOW);
}


// ---------- mixes of A and B ----------
static void roof(GFXcanvas16 *c, int cx, int base, int half, int apex, bool chimney, uint32_t t, bool smoke) {
  const uint16_t ROOF = rgb(214, 98, 74);
  if (chimney) {
    if (smoke)
      for (int i = 0; i < 3; i++) {
        float p = fmodf(t / 2400.0f + i / 3.0f, 1.0f);
        fillEllipse(c, cx + half / 2 + 6 + (int)(p * 10), apex + 4 - (int)(p * 22), 4 + (int)(p * 5), 3 + (int)(p * 4), SOFT);
      }
    c->fillRect(cx + half / 2 - OUT, apex + 8 - OUT, 14 + 2 * OUT, base - apex - 16, INK);
    c->fillRect(cx + half / 2, apex + 8, 14, base - apex - 16, ROOF);
  }
  c->fillTriangle(cx - half - 6, base + 4, cx + half + 6, base + 4, cx, apex - 6, INK);
  c->fillTriangle(cx - half, base, cx + half, base, cx, apex, ROOF);
  c->fillRoundRect(cx - half - 6, base - 2, 2 * half + 12, 7, 3, INK);
}

// M1: the bun wearing a little roof
static void famyyMix1(GFXcanvas16 *c, int cx, uint32_t t) {
  const uint16_t SKIN = rgb(255, 190, 146), DOOR = rgb(255, 224, 192);
  limb(c, cx - 22, 150, cx - 24, 160, SKIN);
  limb(c, cx + 22, 150, cx + 24, 160, SKIN);
  limb(c, cx - 52, 118, cx - 62, 132, SKIN);
  limb(c, cx + 52, 118, cx + 62, 132, SKIN);
  fillEllipse(c, cx, 114, 56 + OUT, 44 + OUT, INK);
  fillEllipse(c, cx, 114, 56, 44, SKIN);
  roof(c, cx, 76, 46, 30, true, t, true);
  fillEllipse(c, cx, 136, 30, 16, DOOR);   // a soft belly, like the bun's
  eyes(c, cx, 102, 40, 8, 12);
  fillEllipse(c, cx - 34, 118, 8, 5, BLUSH);
  fillEllipse(c, cx + 34, 118, 8, 5, BLUSH);
  smile(c, cx, 113, 7, INK);
}

// M2: a soft rounded house, with the bun's sprout growing from its chimney
static void famyyMix2(GFXcanvas16 *c, int cx, uint32_t t) {
  const uint16_t WALL = rgb(255, 190, 146), BELLY = rgb(255, 224, 192), LEAF = rgb(112, 190, 110);
  limb(c, cx - 22, 152, cx - 24, 160, WALL);
  limb(c, cx + 22, 152, cx + 24, 160, WALL);
  limb(c, cx - 50, 120, cx - 60, 134, WALL);
  limb(c, cx + 50, 120, cx + 60, 134, WALL);
  c->fillRoundRect(cx - 50 - OUT, 68 - OUT, 100 + 2 * OUT, 88 + 2 * OUT, 34 + OUT, INK);
  c->fillRoundRect(cx - 50, 68, 100, 88, 34, WALL);
  fillEllipse(c, cx, 136, 28, 16, BELLY);
  roof(c, cx, 74, 50, 30, true, t, false);
  float sway = sinf(t / 500.0f) * 3;   // the sprout, out of the chimney
  int sx = cx + 25 + 7;
  thickLine(c, sx, 38, sx + (int)sway, 20, 5, INK);
  thickLine(c, sx, 38, sx + (int)sway, 20, 2, LEAF);
  fillEllipse(c, sx + (int)sway - 8, 17, 9, 5, INK);
  fillEllipse(c, sx + (int)sway - 8, 17, 6, 3, LEAF);
  fillEllipse(c, sx + (int)sway + 8, 13, 9, 5, INK);
  fillEllipse(c, sx + (int)sway + 8, 13, 6, 3, LEAF);
  eyes(c, cx, 104, 40, 8, 12);
  fillEllipse(c, cx - 33, 120, 8, 5, BLUSH);
  fillEllipse(c, cx + 33, 120, 8, 5, BLUSH);
  smile(c, cx, 115, 7, INK);
}

int main(int argc, char **argv) {
  GFXcanvas16 sheet(320, 400);
  sheet.fillScreen(rgb(60, 56, 52));
  GFXcanvas16 a(320, 196), b(320, 196);
  a.fillScreen(BG);
  famyyHouse(&a, 110, 900);
  drawFo2(&a, 244, ARM_DOWN, ARM_WAVE, -4);
  a.setTextSize(1); a.setTextColor(INK);
  a.setCursor(10, 184); a.print("A  Famyy (home)   +   Fo");
  b.fillScreen(BG);
  drawFo2(&b, 40, ARM_DOWN, ARM_DOWN, 0);
  drawFo2(&b, 122, ARM_DOWN, ARM_WAVE, 2);
  drawFo2(&b, 202, ARM_REACH, ARM_DOWN, -5);
  drawFo2(&b, 284, ARM_UP, ARM_UP, 0);
  b.setTextSize(1); b.setTextColor(INK);
  b.setCursor(10, 184); b.print("stand     wave      handshake    shrug");
  sheet.drawRGBBitmap(0, 0, a.getBuffer(), 320, 196);
  sheet.drawRGBBitmap(0, 200, b.getBuffer(), 320, 196);
  FILE *f = fopen(argv[1], "wb");
  fprintf(f, "P6\n320 400\n255\n");
  const uint16_t *px = sheet.getBuffer();
  for (int i = 0; i < 320 * 400; i++) {
    uint16_t p = px[i];
    uint8_t c3[3] = {(uint8_t)((p >> 11) << 3), (uint8_t)(((p >> 5) & 0x3F) << 2), (uint8_t)((p & 0x1F) << 3)};
    fwrite(c3, 1, 3, f);
  }
  fclose(f);
}
