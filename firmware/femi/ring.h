#pragma once
#include <Arduino.h>
#include <atomic>

// Byte ring buffer for audio, in PSRAM. Safe for exactly one writer task and one reader task,
// which may run on different cores (indices are atomics with acquire/release ordering).
class Ring {
public:
  bool begin(size_t capacity) {
    buf = (uint8_t *)ps_malloc(capacity);
    cap = capacity;
    return buf != nullptr;
  }
  size_t available() const { return head.load(std::memory_order_acquire) - tail.load(std::memory_order_acquire); }
  size_t space() const { return cap - available(); }
  size_t written() const { return head.load(std::memory_order_acquire); }    // bytes ever written
  size_t consumed() const { return tail.load(std::memory_order_acquire); }   // bytes ever read
  // reader side only
  void clear() { tail.store(head.load(std::memory_order_acquire), std::memory_order_release); }

  size_t write(const void *data, size_t n) {   // writer side: returns bytes written, drops the rest
    n = min(n, space());
    size_t h = head.load(std::memory_order_relaxed);
    const uint8_t *p = (const uint8_t *)data;
    for (size_t i = 0; i < n;) {
      size_t at = (h + i) % cap, run = min(n - i, cap - at);
      memcpy(buf + at, p + i, run);
      i += run;
    }
    head.store(h + n, std::memory_order_release);
    return n;
  }

  size_t read(void *data, size_t n) {   // reader side: returns bytes read
    n = min(n, available());
    size_t t = tail.load(std::memory_order_relaxed);
    uint8_t *p = (uint8_t *)data;
    for (size_t i = 0; i < n;) {
      size_t at = (t + i) % cap, run = min(n - i, cap - at);
      memcpy(p + i, buf + at, run);
      i += run;
    }
    tail.store(t + n, std::memory_order_release);
    return n;
  }

private:
  uint8_t *buf = nullptr;
  size_t cap = 0;
  std::atomic<size_t> head{0}, tail{0};   // ever-increasing; wrap is handled by % cap
};
