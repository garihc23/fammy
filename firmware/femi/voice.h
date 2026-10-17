#pragma once
#include <Arduino.h>
#include "ring.h"

// Recording a request (mic → brain) and playing replies (brain → speaker).

// ---- recording: the speech engine's mic feed is tapped while a request is being spoken ----
bool voiceStart(const char *trigger);              // "wake" | "tap" | "followup"; false if busy / no link
void voiceFeed(const int16_t *samples, size_t n);  // called from the mic reader with every 16 kHz sample
bool voiceRecording();
int voiceLevel();                                  // loudness of the last 30 ms, for the listening meter
Ring &micRing();                                   // recorded audio waiting to be sent (network task reads)

// ---- playback through the amp ----
void playerBegin();
Ring &speakRing();                                 // reply audio waiting to be played (network task writes)
void playerSayStart(const char *id, uint32_t bytes);
void playerSayEnd();                               // all of this reply's audio has arrived
bool playerBusy();                                 // still playing
int playerLevel();                                 // loudness right now, to move the mouth
void playerChime(bool happy);                      // short sound cue: happy rising or gentle falling

// sound cues queue up and play between replies (never over one); stale ones are dropped
enum Sfx { SFX_READY = 1, SFX_SOFT, SFX_TICK, SFX_WHOOSH, SFX_CLINK, SFX_DING };
void playerSfx(Sfx s);
void playerClip(int clip);                         // a stored spoken clip (clips.h)
void playerClipOf(int first, int count);           // the next clip of a group, in turn
bool playerQuiet();                                // nothing playing or waiting to play
