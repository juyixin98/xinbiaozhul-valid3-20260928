#pragma once

#include <boost/multiprecision/cpp_int.hpp>
#include <compare>
#include <cstdint>
#include <string>

namespace mileage {

using boost::multiprecision::cpp_int;

// Exact 2D point/vector on an arbitrary-precision integer lattice.
// Input coordinates are parsed as decimal integers, so every geometric
// predicate below is exact regardless of coordinate magnitude.
struct Vec2 {
  cpp_int x;
  cpp_int y;

  bool operator==(const Vec2&) const = default;
};

Vec2 operator-(const Vec2& a, const Vec2& b);
cpp_int dot(const Vec2& a, const Vec2& b);
cpp_int cross(const Vec2& a, const Vec2& b);

// Exact rational num/den with den > 0. Values are intentionally not
// normalized: comparisons cross-multiply, which stays exact either way.
struct Rat {
  cpp_int num;
  cpp_int den{1};

  bool operator==(const Rat& other) const { return cmp(*this, other) == 0; }
  bool operator<(const Rat& other) const { return cmp(*this, other) < 0; }

  static int cmp(const Rat& a, const Rat& b) {
    const cpp_int lhs = a.num * b.den;
    const cpp_int rhs = b.num * a.den;
    if (lhs < rhs) return -1;
    if (lhs > rhs) return 1;
    return 0;
  }
};

// Compare a plain integer against a rational without constructing temporaries
// at call sites. Returns -1/0/+1.
int cmp_int_vs_rat(const cpp_int& n, const Rat& r);

std::string to_string(const cpp_int& v);
std::string to_string(const Rat& r);

// FNV-1a 64-bit hash, used to redact sensitive labels in diagnostics.
uint64_t fnv1a64(const std::string& s);
std::string redact_label(const std::string& label);

}  // namespace mileage
