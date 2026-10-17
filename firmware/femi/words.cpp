#include "words.h"

static const int SIDE_X = 166, SIDE_W = 148;   // the speech bubble beside Famyy
static const int CLOUD_X = 174, CLOUD_W = 134;

Side sideWords(uint32_t t, const Scene &s, const char **text, int *n) {
  const Words &w = *s.w;
  if (s.p->phase != PH_NONE) return SIDE_NONE;   // Fo's visit has the stage
  if (s.face == TALKING && w.reply && w.reply[0]) {
    int len = strlen(w.reply);
    int chars = (int)((uint64_t)(t - w.replySince + 250) * len / max(w.replyMs, (uint32_t)1));   // each word as it's said
    *text = w.reply;
    *n = max(wordsUpTo(w.reply, chars), firstWords(w.reply, 1));
    return SIDE_SPEECH;
  }
  if (s.face == THINKING || s.face == WORKING) {
    bool heard = w.heard && w.heard[0];
    const char *h = heard ? w.heard : w.status;   // a brain without "heard" only sends the status line
    if (!h || !h[0]) return SIDE_NONE;
    *text = h;
    *n = firstWords(h, (t - (heard ? w.heardSince : s.statusSince)) / WORD_MS + 1);
    return SIDE_CLOUD;
  }
  return SIDE_NONE;
}

// a one-line bubble at the top, its tail towards the chimney
static void topBubble(GFXcanvas16 *c, int x, int w, int tailX, const BubbleStyle &st) {
  speechBubble(c, x, 8, w, 28, tailX, 56, st);
}

void drawWords(GFXcanvas16 *c, uint32_t t, const Scene &s, Side side, const char *text, int n, const BubbleStyle &st) {
  const Words &w = *s.w;
  const FoPose &p = *s.p;
  int hx = s.homeX;
  bool aside = hx < 120;   // big bubbles wait until Famyy has stepped aside
  if (side == SIDE_SPEECH && aside) {
    int h = max(bubbleHeight(st, text, n, SIDE_W, 7), 40);
    speechBubble(c, SIDE_X, 172 - h, SIDE_W, h, hx + 50, 156, st);   // the tail points at its mouth
    bubbleText(c, SIDE_X, 172 - h, SIDE_W, text, n, 7, st);
    return;
  }
  if (side == SIDE_CLOUD && aside) {
    int h = max(bubbleHeight(st, text, n, CLOUD_W, 5), 30);
    thoughtCloud(c, CLOUD_X, 112 - h, CLOUD_W, h, hx + 54, 128, st);
    bubbleText(c, CLOUD_X, 112 - h, CLOUD_W, text, n, 5, st);
    return;
  }
  if (p.phase == PH_WORK && w.status && w.status[0]) {   // Fo says what it's doing
    int len = strlen(w.status), shown = min(len, (int)((t - s.statusSince) / TYPE_MS) + 1);
    int bw = min(textWidth(st.font, w.status, len) + 2 * BUBBLE_PAD + 2, 170);
    int bx = constrain(p.foX - bw / 2, 150, 314 - bw);
    int h = bubbleHeight(st, w.status, len, bw, 3);
    speechBubble(c, bx, 6, bw, h, p.foX - 6, 84, st);
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
