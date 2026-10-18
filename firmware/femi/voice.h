#pragma once
#include <Arduino.h>
#include "ring.h"

// Recording a request (mic → brain) and playing replies (brain → speaker).

// ---- recording: the speech engine's mic feed is tapped while a request is being spoken ----
bool voiceStart(const char *trigger);              // "wake" | "tap" | "followup"; false if busy / no link
void voiceFeed(const int16_t *samples, size_t n);  // called from the mic reader with every 16 kHz sample
bool voiceRecording();
void voiceCancel();                                // drop the request being recorded (a tap while listening)
int voiceLevel();                                  // loudness of the last 30 ms, for the listening meter
Ring &micRing();                                   // recorded audio waiting to be sent (network task reads)

// ---- playback through the amp ----
void playerBegin();
Ring &speakRing();                                 // reply audio waiting to be played (network task writes)
// a reply, or one piece of it, as the brain sends it. Several can wait while one plays: each one's words
// show when the speaker reaches its audio, so captions never run ahead of the voice
struct Say {
  char id[16];
  uint32_t bytes;
  int listen;          // listen afterwards: 1 yes, 0 no, -1 only after a question
  bool aside;          // spoken in the middle of something else, which carries on
  int turn;            // the request it belongs to, or -1 (a reminder, ...): a stop drops the rest of that request
  char text[448];
};
bool playerSayStart(const Say &s);                 // false: dropped, it belongs to a request that was stopped
void playerSayEnd();                               // all of the latest reply's audio has arrived
void playerStop(bool quietly = false);             // cut the reply short: what's queued and what's still arriving;
                                                   // quietly: without "Okay, I'll stop." (someone wants to talk)
bool playerDiscarding();                           // the network drops reply audio after a stop, until the next reply
bool playerBusy();                                 // still playing
int playerLevel();                                 // loudness right now, to move the mouth
void playerChime(bool happy);                      // short sound cue: happy rising or gentle falling

// sound cues queue up and play between replies (never over one); stale ones are dropped
enum Sfx {
  SFX_READY = 1, SFX_SOFT, SFX_TICK, SFX_WHOOSH, SFX_CLINK, SFX_DING, SFX_POP, SFX_SPARKLE, SFX_JINGLE,
  SFX_CHIRP, SFX_QUESTION, SFX_BELL, SFX_SHUTTER, SFX_FLIP, SFX_BOOP, SFX_YAWN, SFX_LULLABY, SFX_ALARM,
  SFX_ARCADE, SFX_GLITCH, SFX_TAP, SFX_SPARK
};
void playerSfx(Sfx s);
void playerClip(int clip);                         // a stored spoken clip (clips.h)
void playerClipOf(int first, int count);           // the next clip of a group, in turn
bool playerQuiet();                                // nothing playing or waiting to play
uint32_t playerQuietMs();                          // how long since the speaker last made a sound
void playerMusic(bool on);                         // soft music while Famyy waits; fades in and out, never over speech
