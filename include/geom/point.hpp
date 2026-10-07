#pragma once

// Exact rational 2D geometry primitives backed by Boost.Multiprecision.
// All predicates and constructions in this project are exact (no floating
// point), so degenerate configurations are decided, not approximated.

#include <boost/multiprecision/cpp_int.hpp>

#include <ostream>
#include <stdexcept>
#include <string>

namespace geom {

using boost::multiprecision::cpp_int;
using boost::multiprecision::cpp_rational;

struct Point {
  cpp_rational x;
  cpp_rational y;
};

inline Point operator+(const Point& a, const Point& b) {
  return {a.x + b.x, a.y + b.y};
}
inline Point operator-(const Point& a, const Point& b) {
  return {a.x - b.x, a.y - b.y};
}
inline bool operator==(const Point& a, const Point& b) {
  return a.x == b.x && a.y == b.y;
}

// Vector products (Points double as vectors from the origin).
inline cpp_rational dot(const Point& a, const Point& b) {
  return a.x * b.x + a.y * b.y;
}
inline cpp_rational cross(const Point& a, const Point& b) {
  return a.x * b.y - a.y * b.x;
}
// Oriented area x2 of the triangle (o, a, b).
inline cpp_rational cross(const Point& o, const Point& a, const Point& b) {
  return cross(a - o, b - o);
}

inline int sign(const cpp_rational& v) { return v > 0 ? 1 : (v < 0 ? -1 : 0); }

inline std::string to_string(const cpp_rational& r) {
  const cpp_int num = boost::multiprecision::numerator(r);
  const cpp_int den = boost::multiprecision::denominator(r);
  if (den == 1) return num.str();
  return num.str() + "/" + den.str();
}

inline std::string to_string(const Point& p) {
  return "(" + to_string(p.x) + ", " + to_string(p.y) + ")";
}

inline std::ostream& operator<<(std::ostream& os, const Point& p) {
  return os << to_string(p);
}

// Parses "n", "-n", or "p/q" (integers of arbitrary size).
inline cpp_rational parse_rational(const std::string& s) {
  const auto slash = s.find('/');
  if (slash == std::string::npos) return cpp_rational(cpp_int(s));
  const cpp_int num(s.substr(0, slash));
  const cpp_int den(s.substr(slash + 1));
  if (den == 0) throw std::invalid_argument("zero denominator in: " + s);
  return cpp_rational(num) / cpp_rational(den);
}

}  // namespace geom
