#include <SPI.h>
#include <Adafruit_GFX.h>
#include <Adafruit_ST7789.h>
#include "face.h"
#include "shared.h"
#include "timer.h"
#include "voice.h"

struct Drop { int x; float y, v; };
struct Theme {
  uint16_t ink, dim, rain, tint;   // dim shades are pre-mixed against black: the display has no transparency
  uint16_t good, bad, warn, accent;   // colours for apps (games, morse)
  const char *prompt;
  const char *quips[5];
  const char *listening, *happy, *thinking, *sleeping;
  const char *focus, *brk, *done, *ask;
};

static constexpr uint16_t rgb565(uint8_t r, uint8_t g, uint8_t b) {
  return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3);
}
static const uint16_t BG = 0x0000;
static const uint16_t BLUSH = rgb565(255, 127, 168);

static const Theme NORMAL = {
  rgb565(255, 241, 220), rgb565(89, 84, 77), rgb565(56, 53, 48), rgb565(18, 17, 15),
  rgb565(90, 220, 120), rgb565(255, 90, 90), rgb565(255, 180, 60), rgb565(170, 130, 255),
  "buddy@desk:~$",
  {"idle", "watching you code", "ping 127.0.0.1", "git status: clean", "coffee level: low"},
  "listening", "mood = happy :)", "thinking", "sleep 8h",
  "focus", "break", "time's up!", "how long? (min)",
};
static const Theme HACKER = {
  rgb565(57, 255, 122), rgb565(20, 89, 43), rgb565(13, 56, 27), rgb565(4, 18, 9),
  rgb565(57, 255, 122), rgb565(200, 255, 200), rgb565(30, 170, 80), rgb565(120, 255, 170),   // hacking mode stays green
  "root@desk:~#",
  {"sudo make coffee", "nmap -sV localhost", "tail -f /var/log/life", "ssh buddy@desk", "chmod +x brain.sh"},
  "sniffing audio", "access granted :)", "cracking", "halt",
  "exploit", "cooldown", "pwned! time up", "enter duration:",
};
static const Theme *th = &NORMAL;

// screen zones, each drawn off-screen and pushed in one go
static const int BODY_X = 50, BODY_Y = 24, BODY_W = 220, BODY_H = 172;   // creature zone
static const int BW = 130, BH = 96;                                       // the creature itself
static const int TERM_Y = 204, TERM_H = 36;

static Adafruit_ST7789 tft(&SPI, TFT_CS, TFT_DC, TFT_RST);
static GFXcanvas16 *body, *term;   // allocated in faceBegin(), once the extra memory (PSRAM) is ready
static Drop rain[12];

// animation state
static float gazeX = 0, gazeY = 0, gazeTX = 0, gazeTY = 0;
static uint32_t nextGaze = 0, nextBlink = 2000, blinkStart = 0, nextGlitch = 4000, glitchUntil = 0;
// what is on screen now, so each zone is only redrawn when it changes
static uint32_t lastTerm = 0;
static int shownTermFace = -1, shownTimerKey = -1;
static bool shownRec = false, shownDot = false, shownInvert = false, shownFps = false;

// ---------- drawing helpers ----------

static void thickLine(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w, uint16_t col) {
  bool steep = abs(y1 - y0) > abs(x1 - x0);
  for (int o = -w / 2; o < w - w / 2; o++) {
    if (steep) c->drawLine(x0 + o, y0, x1 + o, y1, col);
    else       c->drawLine(x0, y0 + o, x1, y1 + o, col);
  }
}

static void plusSign(GFXcanvas16 *c, int x, int y, int s, uint16_t col) {
  if (s < 1) return;
  c->fillRect(x - s, y - 1, 2 * s, 3, col);
  c->fillRect(x - 1, y - s, 3, 2 * s, col);
}

// CRT scanlines: halve the brightness of every 3rd row
static void scanlines(GFXcanvas16 *c, int x, int y, int w, int h) {
  uint16_t *buf = c->getBuffer();
  for (int row = max(y, 0); row < min(y + h, (int)c->height()); row += 3) {
    for (int col = max(x, 0); col < min(x + w, (int)c->width()); col++) {
      uint16_t &p = buf[row * c->width() + col];
      p = (p >> 1) & 0x7BEF;   // halves red, green and blue in one go
    }
  }
}

// signal glitch: slide a couple of horizontal strips sideways
static void glitch(GFXcanvas16 *c) {
  uint16_t *buf = c->getBuffer();
  int w = c->width();
  for (int b = 0; b < 2; b++) {
    int y0 = random(c->height() - 10), h = random(3, 10);
    int dx = random(4, 11) * (random(2) ? 1 : -1);
    for (int row = y0; row < y0 + h; row++) {
      uint16_t *r = buf + row * w;
      if (dx > 0) { memmove(r + dx, r, (w - dx) * 2); memset(r, 0, dx * 2); }
      else        { memmove(r, r - dx, (w + dx) * 2); memset(r + w + dx, 0, -dx * 2); }
    }
  }
  c->drawFastHLine(0, random(c->height()), w, th->dim);
}

static float blinkAmount(uint32_t now) {
  uint32_t bt = now - blinkStart;
  return bt < 220 ? max(0.05f, 1.0f - sinf(PI * bt / 220.0f)) : 1.0f;
}

// ---------- screen zones ----------

static void drawBody(uint32_t t, Face f, float blink) {
  GFXcanvas16 *c = body;
  const int cx = BODY_W / 2;
  c->fillScreen(BG);

  // code rain: always on in hacking mode, brighter while thinking
  if (f == THINKING || f == WORKING || th == &HACKER) {
    c->setTextSize(1);
    for (auto &d : rain) {
      d.y += d.v;
      if (d.y > BODY_H + 50) d.y = -10;
      for (int k = 0; k < 5; k++) {
        c->setTextColor(k == 0 && f == THINKING ? th->dim : th->rain);
        c->setCursor(d.x, (int)d.y - k * 10);
        c->print("01abcdef"[((int)d.y / 10 + k * 3 + d.x) & 7]);
      }
    }
  }

  float breath = sinf(t / 700.0f);
  int bob = (f == HAPPY)    ? -(int)(fabsf(sinf(t / 260.0f)) * 8)
          : (f == ALARM)    ? -(int)(fabsf(sinf(t / 150.0f)) * 8)
          : (f == SLEEPING) ? 6
          : (int)(breath * 2);
  int bw = BW, bh = (f == SLEEPING ? BH - 12 : BH) + (int)(breath * 2);
  int bx = cx - bw / 2, by = 104 + bob - bh / 2;
  bool pinging = listening || f == WORKING;   // radar pings: listening to you, or Fo out doing something
  int ax = cx + (pinging ? 0 : (int)(gazeX * 0.4f));

  // antenna, with radar pings while listening
  thickLine(c, cx, by, cx, by - 12, 3, th->ink);
  thickLine(c, cx, by - 12, ax, by - 22, 3, th->ink);
  if (pinging) {
    for (int i = 0; i < 2; i++) {
      float p = fmodf(t / 900.0f + i * 0.5f, 1.0f);
      int r = 7 + (int)(p * 22);
      uint16_t col = p < 0.33f ? th->ink : (p < 0.66f ? th->dim : th->rain);
      c->drawRect(ax - r, by - 28 - r, 2 * r, 2 * r, col);
      c->drawRect(ax - r + 1, by - 27 - r, 2 * r - 2, 2 * r - 2, col);
    }
  }
  c->fillRect(ax - 6, by - 34, 12, 12, th->ink);

  // ears, feet, body outline, and its little screen
  c->fillRoundRect(bx - 6, by + bh / 2 - 12, 6, 24, 2, th->ink);
  c->fillRoundRect(bx + bw, by + bh / 2 - 12, 6, 24, 2, th->ink);
  c->fillRoundRect(bx + 24, by + bh, 20, 8, 2, th->ink);
  c->fillRoundRect(bx + bw - 44, by + bh, 20, 8, 2, th->ink);
  c->fillRoundRect(bx, by, bw, bh, 14, th->ink);
  c->fillRoundRect(bx + 3, by + 3, bw - 6, bh - 6, 11, BG);
  int sx = bx + 14, sy = by + 14, sw = bw - 28, sh = bh - 28;
  c->fillRoundRect(sx, sy, sw, sh, 6, th->dim);
  c->fillRoundRect(sx + 1, sy + 1, sw - 2, sh - 2, 5, th->tint);

  // the face on its screen
  int fx = cx + (int)(gazeX * 0.6f);
  int fy = sy + sh / 2 - 6 + (int)(gazeY * 0.5f) + (f == THINKING ? -6 : 0);
  int eL = fx - 22, eR = fx + 22;
  if (f == SLEEPING) {
    c->fillRect(eL - 9, fy, 18, 4, th->ink);
    c->fillRect(eR - 9, fy, 18, 4, th->ink);
    c->fillRect(fx - 4, fy + 16, 8, 4, th->ink);
  } else if (f == HAPPY) {
    for (int e : {eL, eR}) {
      thickLine(c, e - 9, fy + 4, e, fy - 6, 4, th->ink);
      thickLine(c, e, fy - 6, e + 9, fy + 4, 4, th->ink);
    }
    c->fillRect(eL - 20, fy + 10, 8, 5, BLUSH);
    c->fillRect(eR + 12, fy + 10, 8, 5, BLUSH);
    thickLine(c, fx - 10, fy + 15, fx - 6, fy + 20, 3, th->ink);
    thickLine(c, fx - 6, fy + 20, fx + 6, fy + 20, 3, th->ink);
    thickLine(c, fx + 6, fy + 20, fx + 10, fy + 15, 3, th->ink);
  } else if (f == ALARM) {
    for (int e : {eL, eR}) c->fillRect(e - 9, fy - 12, 18, 24, th->ink);   // wide-eyed
    c->fillRect(fx - 6, fy + 14, 12, 10, th->ink);
    c->fillRect(fx - 3, fy + 17, 6, 4, th->tint);                            // open mouth
  } else if (f == FOCUS) {
    int eh = max(3, (int)(12 * blink));
    for (int e : {eL, eR}) {
      c->fillRect(e - 8, fy - eh / 2, 16, eh, th->ink);                     // narrowed: concentrating
      if (eh > 8) c->fillRect(e + 2, fy - eh / 2 + 2, 3, 3, BG);
    }
    c->fillRect(fx - 5, fy + 18, 10, 3, th->ink);
  } else {
    int eh = max(3, (int)(24 * blink * (f == LISTENING ? 1.15f : 1.0f)));
    for (int e : {eL, eR}) {
      c->fillRect(e - 7, fy - eh / 2, 14, eh, th->ink);
      if (eh > 10) c->fillRect(e + 1, fy - eh / 2 + 3, 4, 4, BG);   // pixel glint
    }
    if (f == IDLE) c->fillRect(fx - 8, fy + 18, 16, 4, th->ink);
    if (f == LISTENING || f == ASKING) {
      c->fillRect(fx - 5, fy + 15, 10, 10, th->ink);
      c->fillRect(fx - 2, fy + 18, 4, 4, th->tint);
    }
    if (f == THINKING || f == WORKING) c->fillRect(fx + 2, fy + 18, 10, 4, th->ink);
    if (f == TALKING) {   // the mouth opens with the loudness of Femi's own voice
      int open = constrain(playerLevel() / 350, 0, 12);
      c->fillRect(fx - 8, fy + 16, 16, 4 + open, th->ink);
      if (open > 3) c->fillRect(fx - 5, fy + 19, 10, open - 2, th->tint);
    }
  }
  scanlines(c, sx, sy, sw, sh);

  if (f == ASKING) {   // a big pixel question mark beside its head
    c->setTextSize(3);
    c->setTextColor(th->ink);
    c->setCursor(bx + bw + 6, by - 6);
    c->print('?');
  }
  if (f == HAPPY) {
    const int sp[4][2] = {{14, 34}, {204, 44}, {200, 150}, {20, 140}};
    for (int i = 0; i < 4; i++) plusSign(c, sp[i][0], sp[i][1], (int)(max(0.0f, sinf(t / 280.0f + i * 2.1f)) * 8), th->ink);
  }
  if (f == ALARM && (t / 200) % 2) {
    for (int x : {14, 202}) {
      c->fillRect(x, 50, 4, 16, th->ink);
      c->fillRect(x, 70, 4, 4, th->ink);
    }
  }
  if (f == SLEEPING) {
    for (int i = 0; i < 3; i++) {
      float p = fmodf(t / 2600.0f + i / 3.0f, 1.0f);
      c->setTextSize(p > 0.5f ? 2 : 1);
      c->setTextColor(p < 0.33f ? th->ink : (p < 0.66f ? th->dim : th->rain));
      c->setCursor(bx + bw + 6 + (int)(p * 20), by + 10 - (int)(p * 40));
      c->print('z');
    }
  }
  if (t < glitchUntil) glitch(c);

  tft.drawRGBBitmap(BODY_X, BODY_Y, c->getBuffer(), BODY_W, BODY_H);
}

static void drawTerm(uint32_t t, Face f, const char *termText) {
  GFXcanvas16 *c = term;
  char buf[32];
  const char *line = "";
  bool timerLine = (f == IDLE || f == FOCUS) && timerCounting();
  if (timerLine) {
    uint32_t secs = (timerLeft(t) + 999) / 1000;
    const char *label = timerPaused() ? "paused" : (timerPhase() == T_FOCUS ? th->focus : th->brk);
    snprintf(buf, sizeof buf, "%s %02lu:%02lu", label, (unsigned long)(secs / 60), (unsigned long)(secs % 60));
    line = buf;
  } else {
    switch (f) {
      case IDLE:
      case FOCUS:     line = th->quips[(t / 4000) % 5]; break;
      case LISTENING: line = th->listening; break;
      case HAPPY:     line = th->happy; break;
      case THINKING:  snprintf(buf, sizeof buf, "%s %c", th->thinking, "|/-\\"[(t / 120) % 4]); line = buf; break;
      case SLEEPING:  line = th->sleeping; break;
      case ALARM:     line = th->done; break;
      case ASKING:    line = th->ask; break;
      case TALKING:
      case WORKING:   line = ""; break;
    }
  }
  if (termText && !timerLine) line = termText;
  c->fillScreen(BG);
  c->setTextSize(2);
  c->setTextColor(th->ink);
  c->setCursor(8, 10);
  c->print("> ");
  c->print(line);
  if ((t / 500) % 2) c->print("_");
  if (timerLine) {   // progress bar: one block per eighth of the time used
    uint32_t total = timerTotal();
    int filled = total ? (int)((total - timerLeft(t)) * 8 / total) : 0;
    for (int i = 0; i < 8; i++) {
      if (i < filled) c->fillRect(222 + i * 12, 12, 9, 14, th->ink);
      else            c->drawRect(222 + i * 12, 12, 9, 14, th->dim);
    }
  }
  if (f == LISTENING) {   // sound-wave meter: your real loudness while Femi records, otherwise decorative
    float loud = voiceRecording() ? constrain((log10f(max(voiceLevel(), 100)) - 2.3f) / 1.7f, 0.1f, 1.0f) : 1.0f;
    for (int i = 0; i < 12; i++) {
      int h = 3 + (int)(fabsf(sinf(t / 120.0f + i * 0.8f) * sinf(t / 330.0f + i)) * 16 * loud);
      c->fillRect(220 + i * 8, 30 - h, 5, h, th->ink);
    }
  }
  tft.drawRGBBitmap(0, TERM_Y, c->getBuffer(), 320, TERM_H);
}

static void drawStatus() {
  tft.fillRect(0, 0, 200, 20, BG);
  tft.setTextSize(1);
  tft.setTextColor(th->dim);
  tft.setCursor(10, 8);
  tft.print(th->prompt);
}

// small countdown in the top bar, visible whatever mood the buddy is in
static void drawStatusTimer(uint32_t now) {
  bool on = timerCounting();
  int secs = on ? (timerLeft(now) + 999) / 1000 : 0;
  int key = on ? secs * 2 + timerPaused() : -1;
  if (key == shownTimerKey) return;
  tft.fillRect(200, 4, 56, 14, BG);
  if (on) {
    char buf[8];
    snprintf(buf, sizeof buf, "%02d:%02d", secs / 60, secs % 60);
    tft.setTextSize(1);
    tft.setTextColor(timerPaused() ? th->dim : th->ink);
    tft.setCursor(214, 8);
    tft.print(buf);
  }
  shownTimerKey = key;
}

static void drawRec(uint32_t now) {
  bool on = listening, dot = on && (now / 300) % 2 == 0;
  if (on == shownRec && dot == shownDot) return;
  tft.fillRect(258, 4, 62, 14, BG);
  if (on) {
    if (dot) tft.fillRect(262, 7, 8, 8, th->ink);
    else     tft.drawRect(262, 7, 8, 8, th->dim);
    tft.setTextSize(1);
    tft.setTextColor(th->ink);
    tft.setCursor(274, 8);
    tft.print("REC");
  }
  shownRec = on;
  shownDot = dot;
}

// ---------- public ----------

void faceBegin() {
  pinMode(SD_CS, OUTPUT);
  digitalWrite(SD_CS, HIGH);     // keep the SD card quiet on the shared bus
  pinMode(TFT_BLK, OUTPUT);
  digitalWrite(TFT_BLK, HIGH);   // backlight on

  SPI.begin(PIN_SCK, PIN_MISO, PIN_MOSI);
  tft.init(240, 320);
  tft.setSPISpeed(TFT_SPI_HZ);
  tft.invertDisplay(false);
  tft.setRotation(1);
  tft.fillScreen(BG);
  drawStatus();

  body = new GFXcanvas16(BODY_W, BODY_H);
  term = new GFXcanvas16(320, TERM_H);
  if (!body->getBuffer() || !term->getBuffer()) Serial.println("canvas alloc FAILED");
  for (int i = 0; i < 12; i++) rain[i] = {4 + i * 18, (float)random(0, 200), 1.5f + random(0, 25) / 10.0f};
}

// theme switch: repaint everything in the new colours
static void applyTheme() {
  const Theme *want = hackerMode ? &HACKER : &NORMAL;
  if (want != th) {
    th = want;
    tft.fillScreen(BG);
    drawStatus();
    shownTermFace = -1;
    shownTimerKey = -2;
    shownRec = !listening;
  }
}

// frame rate while an app runs, in the top bar: shows what a faster display link buys
static void drawFps(uint32_t now) {
  static uint32_t frames = 0, since = 0;
  frames++;
  if (now - since < 500) return;
  char buf[12];
  snprintf(buf, sizeof buf, "%2lu fps", (unsigned long)(frames * 1000 / (now - since)));
  tft.fillRect(140, 4, 58, 14, BG);
  tft.setTextSize(1);
  tft.setTextColor(th->dim);
  tft.setCursor(150, 8);
  tft.print(buf);
  frames = 0;
  since = now;
  shownFps = true;
}

void faceAppFrame(uint32_t now, const App &app, float exitHold) {
  applyTheme();
  drawFps(now);
  uint32_t t0 = micros();
  app.draw(body, Ink{BG, th->ink, th->dim, th->rain, th->good, th->bad, th->warn, th->accent});
  uint32_t t1 = micros();
  tft.drawRGBBitmap(BODY_X, BODY_Y, body->getBuffer(), BODY_W, BODY_H);
  uint32_t t2 = micros();
  if (PROFILE) {   // averages: time drawing into memory vs sending to the display vs the whole frame
    static uint32_t draw = 0, push = 0, frames = 0, since = 0, prev = 0;
    static uint32_t whole = 0;
    if (prev) whole += t0 - prev;
    prev = t0;
    draw += t1 - t0;
    push += t2 - t1;
    if (++frames >= 2 && now - since >= 2000) {
      Serial.printf("frame %lu ms = draw %lu ms + send %lu ms + everything else (%lu fps)\n",
                    whole / (frames - 1) / 1000, draw / frames / 1000, push / frames / 1000,
                    (frames - 1) * 1000000UL / max(whole, 1UL));
      draw = push = whole = frames = 0;
      prev = 0;
      since = now;
    }
  }
  if (now - lastTerm >= 100 || exitHold > 0) {
    term->fillScreen(BG);
    term->setTextSize(2);
    term->setTextColor(th->ink);
    term->setCursor(8, 10);
    term->print("> ");
    if (exitHold > 0) {   // holding the pad: show how close you are to leaving
      term->print("exit");
      for (int i = 0; i < 10; i++) {
        if (i < exitHold * 10) term->fillRect(80 + i * 12, 12, 9, 14, th->warn);
        else                   term->drawRect(80 + i * 12, 12, 9, 14, th->dim);
      }
    } else {
      char buf[32];
      app.status(buf, sizeof buf);
      term->print(buf);
    }
    tft.drawRGBBitmap(0, TERM_Y, term->getBuffer(), 320, TERM_H);
    lastTerm = now;
  }
  shownTermFace = -1;   // the face's own terminal line repaints when the app ends
  drawStatusTimer(now);
  drawRec(now);
}

void faceFrame(uint32_t now, Face f, const char *termText) {
  applyTheme();
  if (shownFps) {   // an app just ended: clear its frame-rate readout
    tft.fillRect(140, 4, 58, 14, BG);
    shownFps = false;
  }

  // whole-screen flash while a finished timer is ringing
  bool inv = f == ALARM && (now / 400) % 2;
  if (inv != shownInvert) {
    tft.invertDisplay(inv);
    shownInvert = inv;
  }

  if (f != SLEEPING && f != LISTENING && f != ALARM && now > nextBlink) {
    blinkStart = now;
    nextBlink = now + (random(5) == 0 ? 400 : random(3000, 6001));   // 1 in 5: double blink
  }
  if (now > nextGaze) {
    gazeTX = random(-14, 15);
    gazeTY = random(-8, 9);
    nextGaze = now + random(1200, 3700);
  }
  float gx = gazeTX, gy = gazeTY;
  if (f == THINKING || f == WORKING) { gx = 16; gy = -12; }
  if (f == ASKING)                  { gx = 10; gy = -8; }   // glancing up, waiting for your answer
  if (f == FOCUS)                   { gx = 0;  gy = 6; }    // eyes down on the work
  if (f == LISTENING || f == ALARM) { gx = 0;  gy = 0; }
  gazeX += (gx - gazeX) * 0.25f;
  gazeY += (gy - gazeY) * 0.25f;

  if (now > nextGlitch) {
    glitchUntil = now + 140;
    nextGlitch = now + (th == &HACKER ? random(1500, 4000) : random(5000, 11000));
  }

  drawBody(now, f, blinkAmount(now));
  if (f != shownTermFace || now - lastTerm >= 100) {
    drawTerm(now, f, termText);
    lastTerm = now;
    shownTermFace = f;
  }
  drawStatusTimer(now);
  drawRec(now);
}

// ---------- Femi's screens ----------

// word-wrap text into lines of at most maxChars, starting at row y; returns rows used
static int drawWrapped(GFXcanvas16 *c, const char *text, int y, int size, int maxChars, int maxLines) {
  const char *p = text;
  int rows = 0;
  while (*p && rows < maxLines) {
    while (*p == ' ') p++;
    int len = strlen(p), cut = min(len, maxChars);
    if (len > maxChars) {   // break at the last space that fits
      int sp = cut;
      while (sp > 0 && p[sp] != ' ') sp--;
      if (sp > 0) cut = sp;
    }
    char line[48];
    snprintf(line, sizeof line, "%.*s", cut, p);
    if (rows == maxLines - 1 && len > cut) {   // out of room: end with dots
      int l = strlen(line);
      strcpy(line + max(0, min(l, maxChars - 3)), "...");
    }
    c->setTextSize(size);
    c->setCursor((c->width() - (int)strlen(line) * 6 * size) / 2, y + rows * 9 * size);
    c->print(line);
    p += cut;
    rows++;
  }
  return rows;
}

static void drawHint(uint32_t now, const char *hint) {
  term->fillScreen(BG);
  term->setTextSize(2);
  term->setTextColor(th->ink);
  term->setCursor(8, 10);
  term->print("> ");
  term->print(hint);
  if ((now / 500) % 2) term->print("_");
  tft.drawRGBBitmap(0, TERM_Y, term->getBuffer(), 320, TERM_H);
}

void faceCardFrame(uint32_t now, bool urgent, const char *title, const char *text, const char *hint, float hold) {
  applyTheme();
  GFXcanvas16 *c = body;
  c->fillScreen(BG);
  uint16_t edge = urgent ? (((now / 500) % 2) ? th->warn : th->bad) : th->ink;   // reminders pulse
  c->drawRoundRect(4, 4, BODY_W - 8, BODY_H - 8, 10, edge);
  c->drawRoundRect(5, 5, BODY_W - 10, BODY_H - 10, 9, edge);
  c->setTextColor(urgent ? th->warn : th->accent);
  drawWrapped(c, title, 16, 2, 16, 1);
  c->setTextColor(th->ink);
  drawWrapped(c, text, 46, 2, 16, 5);
  if (hold > 0) {   // holding the pad: how close to "no"
    c->drawRect(14, BODY_H - 22, BODY_W - 28, 8, th->dim);
    c->fillRect(14, BODY_H - 22, (int)((BODY_W - 28) * min(hold, 1.0f)), 8, th->bad);
  }
  tft.drawRGBBitmap(BODY_X, BODY_Y, c->getBuffer(), BODY_W, BODY_H);
  if (now - lastTerm >= 100) {
    drawHint(now, hint);
    lastTerm = now;
  }
  shownTermFace = -1;   // the face's own terminal line repaints afterwards
  drawStatusTimer(now);
  drawRec(now);
}

void facePhotoFrame(uint32_t now, const uint16_t *pixels, const char *caption, bool fresh) {
  if (!fresh) return;   // a photo is drawn once and then left alone
  if (shownInvert) {
    tft.invertDisplay(false);
    shownInvert = false;
  }
  tft.drawRGBBitmap(0, 0, (uint16_t *)pixels, 320, 240);
  if (caption && caption[0]) {   // caption on a dark band along the bottom
    char line[27];
    snprintf(line, sizeof line, "%s", caption);
    tft.fillRect(0, 214, 320, 26, BG);
    tft.setTextSize(2);
    tft.setTextColor(th->ink);
    tft.setCursor((320 - (int)strlen(line) * 12) / 2, 220);
    tft.print(line);
  }
}

void faceRepaint() {
  tft.fillScreen(BG);
  drawStatus();
  shownTermFace = -1;
  shownTimerKey = -2;
  shownRec = !listening;
  shownFps = false;
}
