#pragma once
#include "config.h"
#include "app.h"

void faceBegin();                       // set up the display and drawing buffers
void faceFrame(uint32_t now, Face f, const char *termText = nullptr);   // one frame of face f; termText replaces the terminal line
void faceCardFrame(uint32_t now, bool urgent, const char *title, const char *text, const char *hint, float hold);   // a card: approval or reminder
void facePhotoFrame(uint32_t now, const uint16_t *pixels, const char *caption, bool fresh);   // a full-screen photo
void faceRepaint();   // after a photo: redraw everything from scratch
void faceAppFrame(uint32_t now, const App &app, float exitHold);   // draw an app instead; exitHold 0..1 shows the hold-to-exit bar
