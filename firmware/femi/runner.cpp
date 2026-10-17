#include "app.h"
#include "settings.h"

// "./debug": the buddy runs, bugs and errors come at it from the right.
// Tap to jump, hold for a higher jump. Flying 0days pass overhead: stay low for those.
// Playfield = the creature zone; units are pixels and seconds.
static const int W = 220, H = 172, GROUND = 150;
static const float GRAVITY = 1800, JUMP = -560, JUMP_CUT = 0.5f;   // letting go early cuts the rise
static const float START_SPEED = 120, MAX_SPEED = 260, SPEEDUP = 5;
static const int BX = 24, BW = 22, BH = 20;                        // the buddy, legs included
static const float STEP = 0.008f;   // physics runs in fixed 8 ms steps however fast the screen redraws

enum Kind { BUG, ERR, STACK, EXPLOIT, NKINDS };   // EXPLOIT flies at head height
struct Obstacle { Kind kind; float x; bool active; };
enum Tone { DEBRIS, DUST };
struct Particle { float x, y, vx, vy; Tone tone; bool active; };
static const int MAX_OBS = 5, MAX_PARTS = 28;

enum State { READY, RUNNING, DYING, OVER };
static State state;
static Obstacle obs[MAX_OBS];
static Particle parts[MAX_PARTS];
static float y, vy, speed, dist, nextSpawnAt, deadY, deadVy;
static bool jumpHeld, inAir;
static int score;
static uint32_t best, last, stateSince, landedAt, shakeUntil;

// ---------- rules ----------

static void box(const Obstacle &o, int &x, int &top, int &w, int &h) {
  x = (int)o.x;
  switch (o.kind) {
    case BUG:     w = 18; h = 10; top = GROUND - h; break;
    case ERR:     w = 22; h = 22; top = GROUND - h; break;
    case STACK:   w = 12; h = 33; top = GROUND - h; break;
    case EXPLOIT: w = 26; h = 12; top = GROUND - BH - 26; break;
    default:      w = h = top = 0; break;
  }
}

static void burst(float x, float y0, int n, Tone tone, float power) {
  for (auto &p : parts) {
    if (n == 0) break;
    if (p.active) continue;
    p = {x, y0, (float)random(-100, 101) / 100 * power, -(float)random(30, 101) / 100 * power, tone, true};
    n--;
  }
}

static void reset(uint32_t now) {
  state = READY;
  stateSince = last = now;
  y = GROUND - BH;
  vy = 0;
  speed = START_SPEED;
  dist = 0;
  nextSpawnAt = 170;
  score = 0;
  jumpHeld = inAir = false;
  for (auto &o : obs) o.active = false;
  for (auto &p : parts) p.active = false;
}

static void jump() {
  vy = JUMP;
  jumpHeld = true;
}

static void begin(uint32_t now) {
  randomSeed(esp_random() ^ micros());   // the moment you tap is never the same twice: new obstacle order every run
  state = RUNNING;
  stateSince = now;
  jump();
}

static void addObstacle(Kind k, float x) {
  for (auto &o : obs) {
    if (o.active) continue;
    o = {k, x, true};
    return;
  }
}

static void spawn() {
  Kind k = (Kind)random(score < 5 ? EXPLOIT : NKINDS);   // flying 0days only once you've warmed up
  addObstacle(k, W + 4);
  if (k == BUG && score >= 3 && random(4) == 0) addObstacle(BUG, W + 30);   // a pair: one long jump clears both
  // leave room to land: a jump covers more ground at higher speed
  float airTime = 2 * -JUMP / GRAVITY;
  nextSpawnAt = dist + speed * airTime + 30 + random(0, 170);
}

static void die(uint32_t now) {
  state = DYING;
  stateSince = now;
  shakeUntil = now + 350;
  deadY = y;
  deadVy = -420;
  burst(BX + BW / 2, y + BH / 2, 20, DEBRIS, 260);
  if ((uint32_t)score > best) {
    best = score;
    settingsPut("runbest", best);
  }
}

static void physics(float dt, const TouchInput &t, uint32_t now) {
  if (state == READY) dist += 40 * dt;   // jog on the spot
  if (state == RUNNING) {
    speed = min(MAX_SPEED, speed + SPEEDUP * dt);
    dist += speed * dt;
    if (jumpHeld && !t.down && vy < 0) {   // let go early: a shorter hop
      vy *= JUMP_CUT;
      jumpHeld = false;
    }
    vy += GRAVITY * dt;
    y += vy * dt;
    if (y >= GROUND - BH) {
      if (inAir) {
        landedAt = now;
        burst(BX + BW / 2, GROUND, 4, DUST, 70);
      }
      y = GROUND - BH;
      vy = 0;
      jumpHeld = false;
    }
    inAir = y < GROUND - BH;

    if (dist >= nextSpawnAt) spawn();
    for (auto &o : obs) {
      if (!o.active) continue;
      o.x -= speed * dt;
      int ox, oy, ow, oh;
      box(o, ox, oy, ow, oh);
      if (ox + ow < 0) {   // dodged
        o.active = false;
        score++;
        continue;
      }
      const int m = 3;   // forgive a few pixels: a near miss should feel like a miss
      if (state == RUNNING && BX + m < ox + ow && BX + BW - m > ox && y + m < oy + oh && y + BH - m > oy) die(now);
    }
  }
  if (state == DYING || state == OVER) {   // the buddy pops up and tumbles off the bottom
    deadVy += GRAVITY * dt;
    deadY += deadVy * dt;
  }
  for (auto &p : parts) {
    if (!p.active) continue;
    p.vy += GRAVITY * 0.6f * dt;
    p.x += p.vx * dt;
    p.y += p.vy * dt;
    if (p.y > H || (p.tone == DUST && p.y > GROUND)) p.active = false;
  }
}

static void start(uint32_t now) {
  best = settingsGet("runbest", 0);
  reset(now);
}

static bool update(uint32_t now, const TouchInput &t) {
  float elapsed = min(now - last, (uint32_t)120) / 1000.0f;
  last = now;
  switch (state) {
    case READY:
      if (t.tapped) begin(now);
      else if (now - stateSince > 20000) return false;   // nobody is playing: back to the buddy
      break;
    case RUNNING:
      if (t.tapped && !inAir) jump();
      break;
    case DYING:
      if (now - stateSince > 1300) {
        state = OVER;
        stateSince = now;
      }
      break;
    case OVER:
      if (t.tapped && now - stateSince > 400) {
        reset(now);
        begin(now);
      } else if (now - stateSince > 15000) {
        return false;
      }
      break;
  }
  for (float done = 0; done < elapsed; done += STEP) physics(min(STEP, elapsed - done), t, now);
  return true;
}

// ---------- drawing ----------

static int wrap(float v, int span) { return ((int)v % span + span) % span; }

static void drawBackground(GFXcanvas16 *c, const Ink &k, uint32_t now) {
  // clouds, drifting slower than the ground: depth without clutter
  for (int i = 0; i < 3; i++) {
    int x = wrap(i * 97 - dist * 0.12f, W + 60) - 30, cy = 18 + i * 13;
    c->fillRoundRect(x, cy, 30, 8, 4, k.far);
    c->fillCircle(x + 10, cy, 6, k.far);
    c->fillCircle(x + 19, cy - 2, 7, k.far);
  }
  // the ground
  c->fillRect(0, GROUND + 1, W, H - GROUND - 1, k.bg);
  c->drawFastHLine(0, GROUND, W, k.ink);
  for (int x = -wrap(dist, 12); x < W; x += 12) c->drawFastHLine(x, GROUND + 5, 6, k.dim);
  for (int x = -wrap(dist, 37); x < W; x += 37) c->fillRect(x + 20, GROUND + 10, 2, 1, k.dim);
}

static void drawBuddy(GFXcanvas16 *c, const Ink &k, uint32_t now) {
  bool dead = state == DYING || state == OVER;
  int w = BW, bodyH = BH - 4, by = (int)(dead ? deadY : y), bx = BX;
  if (!dead && !inAir && now - landedAt < 90) {   // squash on landing
    w += 4; bodyH -= 3; by += 3; bx -= 2;
  } else if (!dead && inAir && vy < -200) {       // stretch on the way up
    w -= 2; bodyH += 2; by -= 2; bx += 1;
  }
  if (by > H) return;
  c->fillRoundRect(bx, by, w, bodyH, 4, k.ink);
  c->fillRect(bx + 2, by + 2, w - 4, bodyH - 4, k.bg);
  if (dead) {   // x_x, legs in the air
    for (int ex : {bx + 7, bx + 13}) {
      c->drawLine(ex, by + 4, ex + 3, by + 7, k.bad);
      c->drawLine(ex, by + 7, ex + 3, by + 4, k.bad);
    }
    c->fillRect(bx + 4, by - 4, 5, 4, k.ink);
    c->fillRect(bx + w - 9, by - 4, 5, 4, k.ink);
    return;
  }
  c->fillRect(bx + w - 12, by + 4, 2, 4, k.ink);   // eyes on the road ahead
  c->fillRect(bx + w - 7, by + 4, 2, 4, k.ink);
  int sway = vy < -100 ? 2 : (vy > 100 ? -2 : 0);   // antenna lags behind the jump
  c->drawLine(bx + 8, by, bx + 4, by - 6 + sway, k.ink);
  c->fillRect(bx + 2, by - 8 + sway, 3, 3, k.warn);
  int legY = by + bodyH, step = ((int)(dist / 8)) % 2;
  c->fillRect(bx + 4, legY, 5, inAir ? 2 : (step ? 4 : 2), k.ink);
  c->fillRect(bx + w - 9, legY, 5, inAir ? 2 : (step ? 2 : 4), k.ink);
}

static void drawObstacle(GFXcanvas16 *c, const Obstacle &o, const Ink &k, uint32_t now) {
  int x, top, w, h;
  box(o, x, top, w, h);
  int wig = ((int)(dist / 5)) % 2;
  switch (o.kind) {
    case BUG: {   // a ladybird-ish beetle, legs scuttling
      c->fillCircle(x + 3, top + 4, 3, k.ink);   // head
      c->drawLine(x + 2, top + 1, x - 2, top - 3 + wig, k.ink);
      c->drawLine(x + 4, top + 1, x + 2, top - 4, k.ink);
      c->fillRoundRect(x + 4, top, w - 4, h - 3, 4, k.bad);
      c->drawFastVLine(x + 4 + (w - 4) / 2, top, h - 3, k.bg);
      c->fillRect(x + 7, top + 2, 2, 2, k.bg);
      c->fillRect(x + 13, top + 4, 2, 2, k.bg);
      for (int i = 0; i < 3; i++) c->drawLine(x + 6 + i * 4, top + h - 3, x + 5 + i * 4 + wig * 2, top + h, k.ink);
      break;
    }
    case ERR: {   // an error dialog, trembling
      int jx = x + ((now / 60) % 2);
      c->fillRect(jx, top, w, h, k.bg);
      c->drawRect(jx, top, w, h, k.ink);
      c->fillRect(jx, top, w, 5, k.bad);
      c->drawLine(jx + w - 5, top + 1, jx + w - 3, top + 3, k.bg);   // the close button
      c->drawLine(jx + w - 5, top + 3, jx + w - 3, top + 1, k.bg);
      c->fillTriangle(jx + w / 2, top + 7, jx + 4, top + h - 3, jx + w - 5, top + h - 3, k.warn);
      c->fillRect(jx + w / 2 - 1, top + 11, 2, 5, k.bg);
      c->fillRect(jx + w / 2 - 1, top + 17, 2, 2, k.bg);
      break;
    }
    case STACK: {   // a stack overflow: blocks piled too high, the top one wobbling
      const uint16_t cols[3] = {k.good, k.warn, k.accent};
      for (int i = 0; i < 3; i++) {
        int bx = x + (i == 0 ? (int)(sinf(now / 120.0f) * 2) : 0);
        c->fillRect(bx, top + i * 11, w, 10, cols[i]);
        c->drawRect(bx, top + i * 11, w, 10, k.ink);
      }
      break;
    }
    case EXPLOIT: {   // a flying 0day, wings flapping, leaving a trail
      for (int i = 1; i <= 3; i++) c->fillRect(x + w + i * 5, top + 5 + (i % 2), 2, 2, i == 1 ? k.dim : k.far);
      c->drawLine(x + 6, top, x + 2, top - 5 + wig * 4, k.ink);
      c->drawLine(x + 20, top, x + 24, top - 5 + wig * 4, k.ink);
      c->fillRect(x, top, w, h, k.accent);
      c->setTextSize(1);
      c->setTextColor(k.bg);
      c->setCursor(x + 1, top + 2);
      c->print("0day");
      c->fillRect(x - 2, top + 4, 2, 3, k.bad);   // a beady red eye
      break;
    }
    default:
      break;
  }
}

static void draw(GFXcanvas16 *c, const Ink &k) {
  uint32_t now = millis();
  c->fillScreen(k.bg);
  drawBackground(c, k, now);
  for (auto &o : obs) {
    if (o.active) drawObstacle(c, o, k, now);
  }
  drawBuddy(c, k, now);
  for (auto &p : parts) {
    if (p.active) c->fillRect((int)p.x, (int)p.y, 2, 2, p.tone == DEBRIS ? ((&p - parts) % 2 ? k.bad : k.warn) : k.dim);
  }

  if (state == READY) {
    c->setTextColor(k.ink);
    drawCentered(c, "tap to run", 2, 40);
    c->setTextColor(k.dim);
    drawCentered(c, "hold for a higher jump", 1, 64);
    drawCentered(c, "stay low for 0days", 1, 76);
  } else if (state == OVER) {
    c->setTextColor(k.bad);
    drawCentered(c, "CORE DUMPED", 2, 40);
    char s[32];
    snprintf(s, sizeof s, "segfault at 0x%04X", score);
    c->setTextColor(k.dim);
    drawCentered(c, s, 1, 62);
    c->setTextColor(k.ink);
    drawCentered(c, "tap to retry", 1, 76);
  } else {
    char s[8];
    snprintf(s, sizeof s, "%d", score);
    c->setTextSize(2);
    c->setTextColor(k.ink);
    c->setCursor(W - 12 * (int)strlen(s) - 6, 4);
    c->print(s);
  }

  if (state == DYING && now - stateSince < 120) {   // red flash on impact
    c->drawRect(0, 0, W, H, k.bad);
    c->drawRect(1, 1, W - 2, H - 2, k.bad);
  }
  if (now < shakeUntil) shakeCanvas(c, random(-4, 5));
}

static void status(char *buf, size_t len) {
  snprintf(buf, len, "./debug  %d  best %lu", score, (unsigned long)best);
}

const App RUNNER_APP = {start, update, draw, status};
