#include "femi.h"

static FemiView view;
static SemaphoreHandle_t lock;
static uint16_t *photo, *snap;

void femiBegin() {
  lock = xSemaphoreCreateMutex();
  photo = (uint16_t *)ps_malloc(PHOTO_W * PHOTO_H * 2);
  snap = (uint16_t *)ps_malloc(SNAP_W * SNAP_H * 2);
}
void femiLock() { xSemaphoreTake(lock, portMAX_DELAY); }
void femiUnlock() { xSemaphoreGive(lock); }
FemiView &femiView() { return view; }
uint16_t *femiPhoto() { return photo; }
uint16_t *femiSnap() { return snap; }

// ---------- JSON ----------

// find `"key":` and return a pointer just past the colon (and spaces), or nullptr
static const char *findKey(const char *json, const char *key) {
  size_t kl = strlen(key);
  for (const char *p = strchr(json, '"'); p; p = strchr(p + 1, '"')) {
    if (strncmp(p + 1, key, kl) == 0 && p[kl + 1] == '"') {
      const char *q = p + kl + 2;
      while (*q == ' ') q++;
      if (*q != ':') continue;
      q++;
      while (*q == ' ') q++;
      return q;
    }
  }
  return nullptr;
}

// the display font is plain ASCII: map common typographic characters, drop the rest
static char foldCodepoint(uint32_t cp) {
  if (cp < 128) return (char)cp;
  switch (cp) {
    case 0x2018: case 0x2019: return '\'';
    case 0x201C: case 0x201D: return '"';
    case 0x2013: case 0x2014: return '-';
    case 0x2026: return '.';
    case 0x00A0: return ' ';
    default: return 0;
  }
}

bool jsonStr(const char *json, const char *key, char *out, size_t n) {
  const char *p = findKey(json, key);
  if (!p || *p != '"' || n == 0) return false;
  p++;
  size_t o = 0;
  while (*p && *p != '"' && o + 1 < n) {
    uint32_t cp;
    if (*p == '\\') {
      p++;
      switch (*p) {
        case 'n': cp = ' '; p++; break;
        case 't': cp = ' '; p++; break;
        case 'u': cp = strtoul(String(p + 1).substring(0, 4).c_str(), nullptr, 16); p += 5; break;
        case 0: cp = 0; break;
        default: cp = (uint8_t)*p++; break;   // \" \\ \/
      }
    } else if ((uint8_t)*p >= 0x80) {   // raw UTF-8 sequence
      uint8_t c = *p;
      int len = c >= 0xF0 ? 4 : c >= 0xE0 ? 3 : 2;
      cp = c & (0x3F >> (len - 1));
      for (int i = 1; i < len && p[i]; i++) cp = (cp << 6) | (p[i] & 0x3F);
      p += len;
    } else {
      cp = (uint8_t)*p++;
    }
    if (cp == 0x2026) {   // an ellipsis becomes three dots
      for (int k = 0; k < 3 && o + 1 < n; k++) out[o++] = '.';
      continue;
    }
    char ch = foldCodepoint(cp);
    if (ch) out[o++] = ch;
  }
  out[o] = 0;
  return true;
}

long jsonInt(const char *json, const char *key, long fallback) {
  const char *p = findKey(json, key);
  return p && (isdigit((uint8_t)*p) || *p == '-') ? strtol(p, nullptr, 10) : fallback;
}

bool jsonBool(const char *json, const char *key, bool fallback) {
  const char *p = findKey(json, key);
  if (!p) return fallback;
  if (strncmp(p, "true", 4) == 0) return true;
  if (strncmp(p, "false", 5) == 0) return false;
  return fallback;
}
