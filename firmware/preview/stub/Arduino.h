// Just enough of Arduino for the drawing code to run on a laptop (see ../README in main.cpp).
#pragma once
#include <cstdint>
#include <cstring>
#include <cstdio>
#include <cstdlib>
#include <cmath>
#include <string>
#include "Print.h"

typedef bool boolean;
#define PROGMEM
#ifndef PI
#define PI 3.14159265358979f
#endif
#define min(a, b) ((a) < (b) ? (a) : (b))
#define max(a, b) ((a) > (b) ? (a) : (b))
#define radians(deg) ((deg) * PI / 180.0f)
#define constrain(x, lo, hi) ((x) < (lo) ? (lo) : ((x) > (hi) ? (hi) : (x)))

inline size_t strlcpy(char *d, const char *s, size_t n) { size_t l = strlen(s); if (n) { size_t k = l < n - 1 ? l : n - 1; memcpy(d, s, k); d[k] = 0; } return l; }
class __FlashStringHelper;
class String {
public:
  String(const char *s = "") : s_(s) {}
  unsigned length() const { return s_.size(); }
  const char *c_str() const { return s_.c_str(); }
private:
  std::string s_;
};
