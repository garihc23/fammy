#pragma once
#include "bubble.h"
#include "fo.h"

// Words on Famyy's stage: what it heard in a thought cloud, what it says in a speech bubble,
// what Fo is doing in Fo's bubble, and small remarks by the chimney. Pure drawing, shared with
// the preview tool; face.cpp gathers what it needs.

// words the face shows in bubbles
struct Words {
  const char *heard;               // what the person said: a thought cloud while Famyy thinks
  uint32_t heardSince;
  const char *status;              // a progress line ("Fo: calling the salon"): Fo's bubble while it works
  const char *reply;               // what Famyy is saying: a speech bubble, a word at a time with its voice
  uint32_t replySince, replyMs;
};

const uint32_t TYPE_MS = 18;                          // a status line types itself out, per character
const uint32_t WORD_MS = 110;                         // heard words appear in the cloud, per word
const int HOME_X = 160, HOME_X_BESIDE = 92;           // where Famyy stands: alone, and beside a big bubble

// what the words need to know this frame
struct Scene {
  Face face;
  const Words *w;
  const FoPose *p;
  int homeX;              // where Famyy stands now
  uint32_t statusSince;   // when the status line last changed
  float meter;            // listening loudness 0..1, or below 0 for no meter
  const char *say;        // a small remark for the top bubble, or nullptr
  const uint16_t *snap;   // the camera's snapshot (SNAP_W x SNAP_H), or nullptr: a polaroid
};

enum Side { SIDE_NONE, SIDE_CLOUD, SIDE_SPEECH };   // the big bubble beside Famyy

// whether there's a big bubble this frame, and which words of it show so far
Side sideWords(uint32_t t, const Scene &s, const char **text, int *n);
void drawWords(GFXcanvas16 *c, uint32_t t, const Scene &s, Side side, const char *text, int n, const BubbleStyle &st);
