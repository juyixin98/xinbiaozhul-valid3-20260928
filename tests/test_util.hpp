#pragma once

#include <cmath>
#include <iostream>
#include <string>

// Minimal self-contained test harness: no external test framework, so the
// project keeps its dependency surface at Boost headers only.
namespace testutil {

inline int& failures() {
  static int f = 0;
  return f;
}
inline int& checks() {
  static int c = 0;
  return c;
}

inline void check(bool ok, const char* expr, const char* file, int line) {
  ++checks();
  if (!ok) {
    ++failures();
    std::cerr << "FAIL " << file << ":" << line << ": " << expr << "\n";
  }
}

inline bool near(long double a, long double b, long double eps) {
  return std::fabs(a - b) <= eps;
}

inline int summary(const char* suite) {
  std::cout << suite << ": " << (checks() - failures()) << "/" << checks()
            << " checks passed\n";
  return failures() == 0 ? 0 : 1;
}

}  // namespace testutil

#define CHECK(cond) testutil::check((cond), #cond, __FILE__, __LINE__)
