#include <Preferences.h>
#include "settings.h"
#include "shared.h"

static Preferences prefs;
static bool savedHacker = false;

void settingsBegin() {
  prefs.begin("buddy", false);
  savedHacker = prefs.getBool("hacker", false);
  hackerMode = savedHacker;
}

void settingsUpdate() {
  // a voice command flips hackerMode; the flash write happens here, outside the speech task
  bool hm = hackerMode;
  if (hm != savedHacker) {
    prefs.putBool("hacker", hm);
    savedHacker = hm;
  }
}

uint32_t settingsGet(const char *key, uint32_t fallback) { return prefs.getUInt(key, fallback); }

void settingsPut(const char *key, uint32_t value) { prefs.putUInt(key, value); }
