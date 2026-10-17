#pragma once
#include <Arduino.h>

// What the brain has asked Femi to show, shared between the network task (writes) and loop() (reads).
// Strings can't be read atomically, so access goes through femiLock()/femiUnlock(); loop() takes a copy.

enum BrainFace { BF_IDLE, BF_THINKING, BF_WORKING, BF_HAPPY, BF_SAD };

struct FemiView {
  bool linkUp;                    // connected to the brain
  bool busy;                      // a request is in flight (from recording until Femi has answered)
  BrainFace face;                 // what the brain says Femi should look like
  char line[40];                  // progress line for the terminal ("Calling the salon…")
  uint32_t faceSince;

  bool speaking;                  // a reply is playing
  char caption[256];              // what Femi is saying, shown in the terminal
  uint32_t speakSince, speakMs;   // when it started, and how long the audio is

  bool approve;                   // waiting for tap (yes) / hold (no)
  char approveId[16];
  char approveText[128];
  uint32_t approveSince;

  bool remind;                    // reminder on screen until tapped
  char remindId[16];
  char remindText[128];

  bool photo;                     // a photo is ready in femiPhoto()
  char photoCaption[96];
  uint32_t photoSince;

  uint8_t foState;                // FoState (fo.h): Fo on the stage
  uint8_t foTask;                 // FoTask: which job icon Fo works under
  uint32_t foCalledAt, foSince;   // when Fo was called; when it finished
};

const int PHOTO_W = 320, PHOTO_H = 240;

void femiBegin();
void femiLock();
void femiUnlock();
FemiView &femiView();          // only touch while locked
uint16_t *femiPhoto();          // PHOTO_W x PHOTO_H RGB565, filled by the network task

// tiny JSON readers for the brain's flat objects: {"type":"approve","id":"a1","text":"…"}
bool jsonStr(const char *json, const char *key, char *out, size_t n);   // decodes escapes, folds to ASCII
long jsonInt(const char *json, const char *key, long fallback);
bool jsonBool(const char *json, const char *key, bool fallback);
