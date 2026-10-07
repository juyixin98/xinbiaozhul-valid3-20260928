#include "geometry/types.hpp"

#include <iomanip>
#include <sstream>

namespace mileage {

Vec2 operator-(const Vec2& a, const Vec2& b) { return {a.x - b.x, a.y - b.y}; }

cpp_int dot(const Vec2& a, const Vec2& b) { return a.x * b.x + a.y * b.y; }

cpp_int cross(const Vec2& a, const Vec2& b) { return a.x * b.y - a.y * b.x; }

int cmp_int_vs_rat(const cpp_int& n, const Rat& r) {
  const cpp_int rhs = r.num;  // n ? r.num / r.den  <=>  n * r.den ? r.num
  const cpp_int lhs = n * r.den;
  if (lhs < rhs) return -1;
  if (lhs > rhs) return 1;
  return 0;
}

std::string to_string(const cpp_int& v) {
  std::ostringstream os;
  os << v;
  return os.str();
}

std::string to_string(const Rat& r) {
  std::ostringstream os;
  os << r.num << "/" << r.den;
  return os.str();
}

uint64_t fnv1a64(const std::string& s) {
  uint64_t h = 14695981039346656037ull;
  for (unsigned char c : s) {
    h ^= c;
    h *= 1099511628211ull;
  }
  return h;
}

std::string redact_label(const std::string& label) {
  if (label.empty()) return "-";
  std::ostringstream os;
  os << "fnv8:" << std::hex << std::setfill('0') << std::setw(8)
     << (fnv1a64(label) & 0xffffffffull);
  return os.str();
}

}  // namespace mileage
