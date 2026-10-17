#include "fo.h"

// Fo as Wajo draws it: thin black line art around a white squircle body, big solid oval eyes set
// close together, a tiny U smile, thin arms with mitten hands (an open hand to wave), short chunky legs.

static const int FAMYY_ASIDE = 112;
static const int FAMYY_HAND = FAMYY_ASIDE + 58, FAMYY_REACH = 30;   // Famyy's right hand, from creature.cpp
static const int FO_HOME = 250, FO_OFFSTAGE = 380;   // Fo's spot, and just off the right edge
static const int FO_W = 54, FO_H = 60, FO_TOP = GROUND_Y - FO_H - 17, FO_REACH = 14;
static const int FO_OUT = 2;   // Fo's line weight: thinner than Famyy's

static float clamp01(float x) { return x < 0 ? 0 : (x > 1 ? 1 : x); }
static float easeOut(float x) { x = clamp01(x); return 1 - (1 - x) * (1 - x) * (1 - x); }
static float easeIn(float x) { x = clamp01(x); return x * x * x; }
static float easeInOut(float x) { x = clamp01(x); return x < 0.5f ? 4 * x * x * x : 1 - powf(-2 * x + 2, 3) / 2; }
static int lerp(int a, int b, float u) { return a + (int)((b - a) * u); }
static int foHandX(int fx, float reach) { return fx - FO_W / 2 - 4 - (int)(reach * FO_REACH); }

FoTask foTaskFromName(const char *s) {
  if (!strcmp(s, "call")) return TASK_CALL;
  if (!strcmp(s, "message")) return TASK_MESSAGE;
  if (!strcmp(s, "email")) return TASK_EMAIL;
  if (!strcmp(s, "shop")) return TASK_SHOP;
  if (!strcmp(s, "calendar")) return TASK_CALENDAR;
  return TASK_WEB;
}

// Fo walking off with the errand in its bag (a background errand), Famyy back to its spot
static FoPose awayPose(uint32_t te, int homeX) {
  FoPose p = {};
  p.famyyX = homeX;
  p.foX = FO_OFFSTAGE;
  if (te >= FO_LEAVE_MS) return p;   // gone: the top bar shows it's out
  p.phase = PH_LEAVE;
  p.tp = te;
  p.lookAtFo = true;
  p.foBag = true;
  p.foWave = te < 600;
  p.foWalk = te >= 300;
  p.foX = lerp(FO_HOME, FO_OFFSTAGE, easeIn((te - 250) / (float)(FO_LEAVE_MS - 250)));
  p.famyyX = lerp(FAMYY_ASIDE, homeX, easeInOut((te - 300) / 600.0f));
  return p;
}

// Fo coming back with a background errand: walks in with its bag, hands over the result (or shrugs), leaves
static FoPose backPose(uint32_t t, bool ok, int homeX) {
  FoPose p = {};
  p.famyyX = homeX;
  p.foX = FO_OFFSTAGE;
  p.lookAtFo = true;
  p.returning = true;
  if (t < FO_ENTER_MS) {
    p.phase = PH_ENTER;
    p.tp = t;
    p.foBag = true;
    p.famyyX = lerp(homeX, FAMYY_ASIDE, easeInOut(t / 600.0f));
    p.foX = lerp(FO_OFFSTAGE, FO_HOME, easeOut(t / (float)FO_ENTER_MS));
    p.foWalk = t < FO_ENTER_MS - 150;
    return p;
  }
  uint32_t te = t - FO_ENTER_MS;
  if (te < FO_HANDOFF_MS) {
    p.famyyX = FAMYY_ASIDE;
    p.foX = FO_HOME;
    p.tp = te;
    if (!ok) {
      p.phase = PH_SHRUG;
      p.foShrug = true;
      return p;
    }
    p.phase = PH_HANDOFF;
    p.foHappy = true;
    p.foReach = te < 350 ? 0.7f : clamp01(0.7f - (te - 350) / 300.0f);
    p.famyyReach = clamp01((te - 250) / 300.0f) * 0.6f;
    int catchX = FAMYY_HAND + (int)(0.6f * FAMYY_REACH);
    if (te < 700) {
      float u = te / 700.0f;
      p.token = true;
      p.tokenX = lerp(foHandX(FO_HOME, 0.7f), catchX, u);
      p.tokenY = CREATURE_HAND_Y - 12 - (int)(4 * 34 * u * (1 - u));
    } else {
      p.sparkle = 2 + (int)((te - 700) / 40);
      p.sparkleX = catchX;
      p.sparkleY = CREATURE_HAND_Y - 10;
    }
    return p;
  }
  uint32_t tl = te - FO_HANDOFF_MS;
  if (tl >= FO_LEAVE_MS) {
    p.lookAtFo = false;
    return p;
  }
  p.phase = PH_LEAVE;
  p.tp = tl;
  p.foWave = tl < 600;
  p.foWalk = tl >= 300;
  p.foX = lerp(FO_HOME, FO_OFFSTAGE, easeIn((tl - 250) / (float)(FO_LEAVE_MS - 250)));
  p.famyyX = lerp(FAMYY_ASIDE, homeX, easeInOut((tl - 300) / 600.0f));
  return p;
}

FoPose foPose(uint32_t now, FoState state, uint32_t calledAt, uint32_t since, int homeX) {
  if (state == FO_AWAY) return awayPose(now - since, homeX);
  if (state == FO_BACK || state == FO_BACK_FAILED) return backPose(now - since, state == FO_BACK, homeX);
  FoPose p = {};
  p.famyyX = homeX;
  p.foX = FO_OFFSTAGE;
  if (state == FO_OFF) return p;
  uint32_t t = now - calledAt;
  const uint32_t workAt = FO_ENTER_MS + FO_SHAKE_MS;
  p.lookAtFo = true;

  // the arrival and the handshake always play in full, even if Fo answers very quickly
  if (state == FO_WORKING || t < workAt) {
    p.famyyX = lerp(homeX, FAMYY_ASIDE, easeInOut(t / 600.0f));
    if (t < FO_ENTER_MS) {
      p.phase = PH_ENTER;
      p.tp = t;
      p.foX = lerp(FO_OFFSTAGE, FO_HOME, easeOut(t / (float)FO_ENTER_MS));
      p.foWalk = t < FO_ENTER_MS - 150;
      p.foWave = t > 500;   // hello!
    } else if (t < workAt) {
      p.phase = PH_SHAKE;
      p.tp = t - FO_ENTER_MS;
      p.foX = FO_HOME;
      p.foHappy = true;
      float tp = p.tp;
      float reach = tp < 200 ? tp / 200 : (tp > 650 ? (FO_SHAKE_MS - tp) / 150 : 1);
      p.famyyReach = p.foReach = clamp01(reach);
      if (tp >= 200 && tp < 650) {
        p.handDy = (int)(sinf((tp - 200) / 72.0f) * 5);   // three shakes
        p.sparkle = 3 + (int)(fabsf(sinf((tp - 200) / 60.0f)) * 5);
        p.sparkleX = (FAMYY_HAND + FAMYY_REACH + foHandX(FO_HOME, 1)) / 2;
        p.sparkleY = CREATURE_HAND_Y;
      }
    } else {
      p.phase = PH_WORK;
      p.tp = t - workAt;
      p.foX = FO_HOME;
    }
    return p;
  }

  // finished: hand the result over (or shrug), then leave
  uint32_t endAt = max(since, calledAt + workAt);
  uint32_t te = now - endAt;
  if (te < FO_HANDOFF_MS) {
    p.famyyX = FAMYY_ASIDE;
    p.foX = FO_HOME;
    p.tp = te;
    if (state == FO_FAILED) {
      p.phase = PH_SHRUG;
      p.foShrug = true;
      return p;
    }
    p.phase = PH_HANDOFF;
    p.foHappy = true;
    p.foReach = te < 350 ? 0.7f : clamp01(0.7f - (te - 350) / 300.0f);
    p.famyyReach = clamp01((te - 250) / 300.0f) * 0.6f;
    int catchX = FAMYY_HAND + (int)(0.6f * FAMYY_REACH);
    if (te < 700) {   // the result arcs over from Fo's hand to Famyy's
      float u = te / 700.0f;
      p.token = true;
      p.tokenX = lerp(foHandX(FO_HOME, 0.7f), catchX, u);
      p.tokenY = CREATURE_HAND_Y - 12 - (int)(4 * 34 * u * (1 - u));
    } else {
      p.sparkle = 2 + (int)((te - 700) / 40);   // and bursts into sparkles when caught
      p.sparkleX = catchX;
      p.sparkleY = CREATURE_HAND_Y - 10;
    }
    return p;
  }
  uint32_t tl = te - FO_HANDOFF_MS;
  if (tl < FO_LEAVE_MS) {
    p.phase = PH_LEAVE;
    p.tp = tl;
    p.foWave = tl < 600;
    p.foWalk = tl >= 300;
    p.foX = lerp(FO_HOME, FO_OFFSTAGE, easeIn((tl - 250) / (float)(FO_LEAVE_MS - 250)));
    p.famyyX = lerp(FAMYY_ASIDE, homeX, easeInOut((tl - 300) / 600.0f));
    return p;
  }
  p.lookAtFo = false;
  return p;   // PH_NONE: everyone back where they were
}

void foPlaceFamyy(const FoPose &p, Look &k) {
  k.cx = p.famyyX;
  k.reach = p.famyyReach;
  k.handDy = p.handDy;
  if (p.lookAtFo) {
    k.gazeX = 14;
    k.gazeY = p.phase == PH_WORK ? -10 : 0;   // watching Fo's job icon while it works
  }
  if (p.phase == PH_SHAKE || (p.phase == PH_HANDOFF && p.tp > 700)) k.face = HAPPY;   // glad to see Fo, and to get the result
}

// ---------- drawing ----------

static void tube(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w, const Palette &pal) {   // a white tube, thinly outlined
  thickLine(c, x0, y0, x1, y1, w + 2 * FO_OUT, pal.foInk);
  c->fillCircle(x0, y0, (w + 2 * FO_OUT) / 2, pal.foInk);
  c->fillCircle(x1, y1, (w + 2 * FO_OUT) / 2, pal.foInk);
  thickLine(c, x0, y0, x1, y1, w, pal.fo);
  c->fillCircle(x1, y1, w / 2, pal.fo);
}

static void blob(GFXcanvas16 *c, int x, int y, int rx, int ry, const Palette &pal) {
  fillEllipse(c, x, y, rx + FO_OUT, ry + FO_OUT, pal.foInk);
  fillEllipse(c, x, y, rx, ry, pal.fo);
}

static void openHand(GFXcanvas16 *c, int x, int y, float dir, const Palette &pal) {   // palm, four fingers spread, thumb out
  const float spread[5] = {-0.56f, -0.19f, 0.19f, 0.56f, 1.45f};
  const int len[5] = {13, 15, 15, 13, 10};
  for (int pass = 0; pass < 2; pass++)
    for (int i = 0; i < 5; i++) {
      int ex = x + (int)(cosf(dir + spread[i]) * len[i]), ey = y + (int)(sinf(dir + spread[i]) * len[i]);
      if (pass == 0) {
        thickLine(c, x, y, ex, ey, 4, pal.foInk);
        c->fillCircle(ex, ey, 2, pal.foInk);
      } else {
        thickLine(c, x, y, ex, ey, 2, pal.fo);
        c->drawPixel(ex, ey, pal.fo);
      }
    }
  fillEllipse(c, x, y, 6, 6, pal.foInk);
  fillEllipse(c, x, y, 5, 5, pal.fo);
}

static void drawTaskIcon(GFXcanvas16 *c, int x, int y, FoTask task, uint32_t now, uint16_t col, uint16_t bg) {
  switch (task) {
    case TASK_CALL: {   // a phone, with its signal pulsing out
      c->fillRoundRect(x - 6, y - 10, 12, 20, 3, col);
      c->fillRect(x - 4, y - 7, 8, 11, bg);
      c->fillRect(x - 1, y + 6, 2, 2, bg);
      int arcs = (now / 280) % 4;
      for (int i = 1; i <= min(arcs, 3); i++) {
        c->drawCircleHelper(x + 7, y - 4, 4 * i, 0x2 | 0x4, col);
        c->drawCircleHelper(x + 7, y - 4, 4 * i + 1, 0x2 | 0x4, col);
      }
      break;
    }
    case TASK_MESSAGE: {   // a chat bubble, someone typing
      c->drawRoundRect(x - 12, y - 9, 24, 16, 5, col);
      c->drawRoundRect(x - 11, y - 8, 22, 14, 4, col);
      c->fillTriangle(x - 7, y + 6, x - 1, y + 6, x - 9, y + 12, col);
      int dots = (now / 250) % 4;
      for (int i = 0; i < dots; i++) c->fillRect(x - 7 + i * 6, y - 2, 3, 3, col);
      break;
    }
    case TASK_EMAIL: {   // an envelope, flap opening and closing
      c->drawRect(x - 12, y - 8, 24, 16, col);
      c->drawRect(x - 11, y - 7, 22, 14, col);
      int flap = (int)(fabsf(sinf(now / 350.0f)) * 7);
      c->drawLine(x - 11, y - 7, x, y + 1 - flap, col);
      c->drawLine(x + 11, y - 7, x, y + 1 - flap, col);
      break;
    }
    case TASK_SHOP: {   // a little cart rolling on the spot
      int dx = (int)(sinf(now / 200.0f) * 2);
      thickLine(c, x - 13 + dx, y - 8, x - 9 + dx, y - 8, 2, col);
      thickLine(c, x - 9 + dx, y - 8, x - 5 + dx, y + 3, 2, col);
      thickLine(c, x - 5 + dx, y + 3, x + 9 + dx, y + 3, 2, col);
      thickLine(c, x + 9 + dx, y + 3, x + 12 + dx, y - 5, 2, col);
      thickLine(c, x + 12 + dx, y - 5, x - 7 + dx, y - 5, 2, col);
      c->fillCircle(x - 3 + dx, y + 8, 2, col);
      c->fillCircle(x + 7 + dx, y + 8, 2, col);
      break;
    }
    case TASK_CALENDAR: {   // a calendar page, days ticking by
      c->drawRect(x - 11, y - 8, 22, 19, col);
      c->fillRect(x - 11, y - 8, 22, 5, col);
      c->fillRect(x - 6, y - 12, 2, 5, col);
      c->fillRect(x + 4, y - 12, 2, 5, col);
      int lit = (now / 300) % 6;
      for (int i = 0; i < 6; i++) {
        int dx = x - 7 + (i % 3) * 6, dy = y - 1 + (i / 3) * 6;
        if (i == lit) c->fillRect(dx - 1, dy - 1, 5, 5, col);
        else c->fillRect(dx, dy, 3, 3, col);
      }
      break;
    }
    default: {   // a spinning globe: Fo looking things up
      c->drawCircle(x, y, 11, col);
      c->drawCircle(x, y, 10, col);
      c->drawFastHLine(x - 10, y, 21, col);
      int w = 2 + (int)(fabsf(sinf(now / 300.0f)) * 14);
      c->drawRoundRect(x - w / 2, y - 10, w, 21, w / 2, col);
      break;
    }
  }
}

static void drawToken(GFXcanvas16 *c, int x, int y, const Palette &pal) {   // a ticked circle: done!
  c->fillCircle(x, y, 9, pal.ink);
  c->fillCircle(x, y, 7, pal.good);
  thickLine(c, x - 4, y, x - 1, y + 3, 2, 0xFFFF);
  thickLine(c, x - 1, y + 3, x + 4, y - 3, 2, 0xFFFF);
}

void foDraw(GFXcanvas16 *c, uint32_t now, const FoPose &p, FoTask task, const Palette &pal, const GFXfont *nameFont) {
  if (p.sparkle > 0) {
    const int sp[3][2] = {{-14, -16}, {12, -18}, {2, -28}};
    for (int i = 0; i < 3; i++) plusSign(c, p.sparkleX + sp[i][0], p.sparkleY + sp[i][1], min(p.sparkle - i, 9), pal.accent);
  }
  if (p.token) drawToken(c, p.tokenX, p.tokenY, pal);
  int fx = p.foX;
  if (fx - FO_W > c->width()) return;

  float step = sinf(now / 85.0f);
  int bob = p.foWalk ? -(int)(fabsf(step) * 3) : 0;
  int top = FO_TOP + bob, sy = top + 30;   // body top, shoulder height

  // short chunky legs with little feet, stepping while it walks
  int liftL = p.foWalk ? max(0, (int)(step * 5)) : 0, liftR = p.foWalk ? max(0, (int)(-step * 5)) : 0;
  tube(c, fx - 9, top + FO_H - 6, fx - 10, top + FO_H + 9 - liftL, 9, pal);
  tube(c, fx + 9, top + FO_H - 6, fx + 10, top + FO_H + 9 - liftR, 9, pal);
  blob(c, fx - 13, top + FO_H + 12 - liftL, 7, 3, pal);
  blob(c, fx + 13, top + FO_H + 12 - liftR, 7, 3, pal);

  // arms: the left reaches for Famyy, the right waves with an open hand; both go up for a shrug
  int lx = fx - FO_W / 2 + 2, rx = fx + FO_W / 2 - 2;
  if (p.foReach > 0.02f) {
    int hx = foHandX(fx, p.foReach), hy = CREATURE_HAND_Y + p.handDy;
    tube(c, lx, sy, hx, hy, 4, pal);
    blob(c, hx - 2, hy, 5, 5, pal);
  } else if (p.foShrug) {
    tube(c, lx, sy - 2, lx - 12, sy - 18, 4, pal);
    blob(c, lx - 13, sy - 21, 4, 4, pal);
  } else {
    tube(c, lx, sy, lx - 8, sy + 14, 4, pal);
    blob(c, lx - 8, sy + 17, 4, 4, pal);
  }
  if (p.foWave) {
    int sway = (int)(sinf(now / 90.0f) * 3);
    tube(c, rx, sy - 2, rx + 11 + sway, sy - 18, 4, pal);
    openHand(c, rx + 13 + sway, sy - 24, atan2f(-16, 3 + sway), pal);
  } else if (p.foShrug) {
    tube(c, rx, sy - 2, rx + 12, sy - 18, 4, pal);
    blob(c, rx + 13, sy - 21, 4, 4, pal);
  } else {
    tube(c, rx, sy, rx + 8, sy + 14, 4, pal);
    blob(c, rx + 8, sy + 17, 4, 4, pal);
  }

  if (nameFont) nameChip(c, fx, "Fo", nameFont, pal);

  if (p.foBag && !p.foWave) {   // the errand, in a little satchel at its side
    int bx = rx + 6, by = sy + 18;
    c->drawCircleHelper(bx + 6, by, 5, 0x1 | 0x2, pal.foInk);   // the handle
    c->fillRoundRect(bx - 1, by - 1, 15, 13, 3, pal.foInk);
    c->fillRoundRect(bx + 1, by + 1, 11, 9, 2, 0xB46A);        // tan leather
    c->drawFastHLine(bx + 1, by + 4, 11, pal.foInk);            // the flap
  }

  // the body: a soft white squircle
  c->fillRoundRect(fx - FO_W / 2 - FO_OUT, top - FO_OUT, FO_W + 2 * FO_OUT, FO_H + 2 * FO_OUT, 22 + FO_OUT, pal.foInk);
  c->fillRoundRect(fx - FO_W / 2, top, FO_W, FO_H, 22, pal.fo);

  // face: big solid ovals set close, a small U smile under them
  bool working = p.phase == PH_WORK;
  int look = (p.phase == PH_ENTER || p.phase == PH_SHAKE || p.phase == PH_HANDOFF) ? -5 : (working ? -2 : 0);
  int ex = fx + look, ey = top + 22 + (working ? -3 : 0);
  bool blink = (now + 700) % 3400 < 120;
  fillEllipse(c, ex - 8, ey, 5, blink ? 1 : 8, pal.foInk);
  fillEllipse(c, ex + 8, ey, 5, blink ? 1 : 8, pal.foInk);
  if (p.foShrug) {
    c->fillRect(ex - 4, ey + 12, 9, 2, pal.foInk);   // a flat "sorry" mouth
  } else {
    int r = p.foHappy ? 6 : 5;
    arc(c, ex, ey + 9, r, 0.12f, 0.88f, 2, pal.foInk);
  }

  // while it works: the job's icon above its head, with three dots circling it
  if (working) {
    int ix = fx, iy = top - 30 + (int)(sinf(now / 400.0f) * 2);
    drawTaskIcon(c, ix, iy, task, now, pal.ink, pal.bg);
    for (int i = 0; i < 3; i++) {
      float a = now / 260.0f + i * 2.094f;
      c->fillRect(ix + (int)(cosf(a) * 21) - 1, iy + (int)(sinf(a) * 15) - 1, 3, 3, pal.accent);
    }
  }
  if (p.foShrug) {   // a small "?" over its head
    c->setTextSize(2);
    c->setTextColor(pal.ink);
    c->setCursor(fx - 5, top - 24);
    c->print('?');
  }
}
