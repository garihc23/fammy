#include "words.h"

static const int SIDE_X = 166, SIDE_W = 148;   // the speech bubble beside Famyy
static const int CLOUD_X = 174, CLOUD_W = 134;
// heights on the stage, from the floor up: Famyy's mouth, its shoulder, Fo's job icon, Famyy's chimney
static const int MOUTH_Y = GROUND_Y - 49, SHOULDER_Y = GROUND_Y - 77, FO_ICON_Y = GROUND_Y - 107, CHIMNEY_Y = GROUND_Y - 149;

Side sideWords(uint32_t t, const Scene &s, const char **text, int *n) {
  const Words &w = *s.w;
  bool fo = s.p->phase != PH_NONE;
  if (fo && !(s.p->phase == PH_LEAVE && s.face == TALKING)) return SIDE_NONE;   // Fo's visit has the stage, until it walks off
  if (s.face == TALKING && w.reply && w.reply[0]) {
    int len = strlen(w.reply);
    int chars = (int)((uint64_t)(t - w.replySince + 250) * len / max(w.replyMs, (uint32_t)1));   // each word as it's said
    *text = w.reply;
    *n = max(wordsUpTo(w.reply, chars), firstWords(w.reply, 1));
    return SIDE_SPEECH;
  }
  if (!fo && (s.face == THINKING || s.face == WORKING)) {
    // the cloud is there as soon as you stop talking, "..." until your words arrive, then they fill in quickly
    bool heard = w.heard && w.heard[0];
    const char *h = heard ? w.heard : w.status;   // a brain without "heard" only sends the status line
    *text = h ? h : "";
    if (!**text) {
      *n = 0;
      return SIDE_CLOUD;
    }
    int words = max(1, firstWordsCount(*text));
    uint32_t perWord = min(WORD_MS, 700 / (uint32_t)words);   // the whole sentence within ~0.7 s
    *n = firstWords(*text, (t - (heard ? w.heardSince : s.statusSince)) / max(perWord, (uint32_t)1) + 1);
    return SIDE_CLOUD;
  }
  return SIDE_NONE;
}

// a polaroid of the camera's snapshot: a white frame, deeper at the bottom, a strip of tape on top.
// step 2 shows it at half size (every other pixel)
static void polaroid(GFXcanvas16 *c, int x, int y, const uint16_t *px, int step, const BubbleStyle &st) {
  const uint16_t TAPE = 0xF71A;   // a pale, warm yellow
  int w = SNAP_W / step, h = SNAP_H / step, b = step == 1 ? 6 : 3, foot = step == 1 ? 18 : 8;
  c->fillRoundRect(x - b - 1, y - b - 1, w + 2 * b + 2, h + b + foot + 2, 3, st.ink);
  c->fillRoundRect(x - b, y - b, w + 2 * b, h + b + foot, 2, 0xFFFF);
  if (step == 1) {
    c->drawRGBBitmap(x, y, (uint16_t *)px, w, h);
  } else {
    for (int yy = 0; yy < h; yy++)
      for (int xx = 0; xx < w; xx++) c->drawPixel(x + xx, y + yy, px[yy * step * SNAP_W + xx * step]);
  }
  c->fillRect(x + w / 2 - 10 / step - 6, y - b - 5 / step - 2, 24 / step + 8, 9 / step + 3, TAPE);
  if (step == 1) {   // a handwritten-looking note under the picture
    c->setFont(st.font);
    c->setTextColor(st.ink);
    const char *note = "just now";
    c->setCursor(x + (w - textWidth(st.font, note, strlen(note))) / 2, y + h + 15);
    c->print(note);
    c->setFont(nullptr);
  }
}

// a one-line bubble at the top, its tail towards the chimney
static void topBubble(GFXcanvas16 *c, int x, int w, int tailX, const BubbleStyle &st) {
  speechBubble(c, x, 8, w, 28, tailX, CHIMNEY_Y, st);
}

void drawWords(GFXcanvas16 *c, uint32_t t, const Scene &s, Side side, const char *text, int n, const BubbleStyle &st) {
  const Words &w = *s.w;
  const FoPose &p = *s.p;
  int hx = s.homeX;
  bool aside = hx < 120;   // big bubbles wait until Famyy has stepped aside
  if (s.snap) {
    if (side == SIDE_CLOUD && aside) {   // thinking about the photo: it takes the cloud's place
      polaroid(c, 186, 16, s.snap, 1, st);
      return;
    }
    polaroid(c, 8, 8, s.snap, 2, st);   // otherwise it stays small in the corner while the talk goes on
  }
  if (side == SIDE_SPEECH && aside) {
    int h = max(bubbleHeight(st, text, n, SIDE_W, 7), 40);
    int bottom = MOUTH_Y + 16;
    speechBubble(c, SIDE_X, bottom - h, SIDE_W, h, hx + 50, MOUTH_Y, st);   // the tail points at its mouth
    bubbleText(c, SIDE_X, bottom - h, SIDE_W, text, n, 7, st);
    return;
  }
  if (side == SIDE_CLOUD && aside) {
    int h = max(bubbleHeight(st, text, n, CLOUD_W, 5), 30);
    int bottom = SHOULDER_Y - 16;
    thoughtCloud(c, CLOUD_X, bottom - h, CLOUD_W, h, hx + 54, SHOULDER_Y, st);
    if (n > 0) {
      bubbleText(c, CLOUD_X, bottom - h, CLOUD_W, text, n, 5, st);
    } else {   // still working out what you said: three dots, bouncing
      for (int i = 0; i < 3; i++) {
        int dy = (int)(fabsf(sinf(t / 180.0f - i * 0.7f)) * 5);
        c->fillCircle(CLOUD_X + CLOUD_W / 2 - 16 + i * 16, bottom - h / 2 - dy, 4, st.ink);
      }
    }
    return;
  }
  if (p.phase == PH_WORK && w.status && w.status[0]) {   // Fo says what it's doing
    int len = strlen(w.status), shown = min(len, (int)((t - s.statusSince) / TYPE_MS) + 1);
    int bw = min(textWidth(st.font, w.status, len) + 2 * BUBBLE_PAD + 2, 170);
    int bx = constrain(p.foX - bw / 2, 150, 314 - bw);
    int h = bubbleHeight(st, w.status, len, bw, 3);
    speechBubble(c, bx, 6, bw, h, p.foX - 6, FO_ICON_Y - 14, st);
    bubbleText(c, bx, 6, bw, w.status, shown, 3, st);
    return;
  }
  if (p.phase != PH_NONE) return;

  // small things, in a bubble by the chimney
  int tail = hx + 34;
  if (s.meter >= 0) {   // a sound meter while Famyy listens
    topBubble(c, hx + 20, 96, tail, st);
    for (int i = 0; i < 9; i++) {
      int bh = 3 + (int)(fabsf(sinf(t / 120.0f + i * 0.8f) * sinf(t / 330.0f + i)) * 16 * s.meter);
      c->fillRoundRect(hx + 32 + i * 9, 22 - bh / 2, 5, bh, 2, st.ink);
    }
    return;
  }
  if (!s.say) return;
  int len = strlen(s.say);
  int bw = textWidth(st.font, s.say, len) + 2 * BUBBLE_PAD + 2;
  int bx = constrain(hx + 20, 4, 316 - bw);
  topBubble(c, bx, bw, tail, st);
  bubbleText(c, bx, 8, bw, s.say, len, 1, st);
}
