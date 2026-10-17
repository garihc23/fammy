#pragma once
#include <Adafruit_GFX.h>

// Words on the stage, comic style: speech bubbles, thought clouds, and text that wraps and
// appears a word at a time. Pure drawing, shared with the preview tool.

struct BubbleStyle {
  uint16_t fill, ink, text;          // bubble inside, its outline, the words
  const GFXfont *font;               // a proportional font (FreeSans), or nullptr for the built-in one
};

const int BUBBLE_LINE_H = 17;   // distance between lines of bubble text
const int BUBBLE_PAD = 8;       // space between the outline and the words

struct TextLine { const char *start; int len; };

// splits the first n characters of text into lines no wider than width; returns how many lines
int wrapText(const GFXfont *font, const char *text, int n, int width, TextLine *lines, int maxLines);
int textWidth(const GFXfont *font, const char *s, int n);

// how much of a text to show: whole words, so a word never appears letter by letter
int wordsUpTo(const char *text, int chars);                        // round a cut forward to the end of its word
int firstWords(const char *text, int words);                       // characters in the first n words
int firstWordsCount(const char *text);                             // how many words there are

// the shapes
void speechBubble(GFXcanvas16 *c, int x, int y, int w, int h, int tailX, int tailY, const BubbleStyle &s);
void thoughtCloud(GFXcanvas16 *c, int x, int y, int w, int h, int trailX, int trailY, const BubbleStyle &s);

// a block of text in a bubble: the last maxLines lines of the first n characters, so long text
// scrolls up as it comes; returns the bubble height it needs (0 when there's nothing to show)
int bubbleHeight(const BubbleStyle &s, const char *text, int n, int w, int maxLines);
void bubbleText(GFXcanvas16 *c, int x, int y, int w, const char *text, int n, int maxLines, const BubbleStyle &s);
