#include "mer/types.hpp"

#include <algorithm>
#include <boost/multiprecision/cpp_int.hpp>
#include <stdexcept>

namespace mer {

namespace {

Integer gcd_integer(Integer a, Integer b) {
  a = boost::multiprecision::abs(a);
  b = boost::multiprecision::abs(b);
  while (b != 0) {
    Integer rem = a % b;
    a = b;
    b = rem;
  }
  return a;
}

}  // namespace

Fraction::Fraction(Integer n, Integer d) : num(std::move(n)), den(std::move(d)) {
  if (den == 0) {
    throw std::invalid_argument("fraction denominator is zero");
  }
  if (den < 0) {
    num = -num;
    den = -den;
  }
  Integer g = gcd_integer(num, den);
  if (g != 0) {
    num /= g;
    den /= g;
  }
}

Decimal Fraction::value() const {
  return decimal_from_integer(num) / decimal_from_integer(den);
}

bool Fraction::operator==(const Fraction& other) const {
  return num * other.den == other.num * den;
}

Integer integer_from_coord(Coord value) {
  return Integer{value};
}

Integer cross(const Point& a, const Point& b, const Point& c) {
  Integer bx = integer_from_coord(b.x) - integer_from_coord(a.x);
  Integer by = integer_from_coord(b.y) - integer_from_coord(a.y);
  Integer cx = integer_from_coord(c.x) - integer_from_coord(a.x);
  Integer cy = integer_from_coord(c.y) - integer_from_coord(a.y);
  return bx * cy - by * cx;
}

Integer dot_delta(const Point& origin, const Point& p, Coord dx, Coord dy) {
  Integer px = integer_from_coord(p.x) - integer_from_coord(origin.x);
  Integer py = integer_from_coord(p.y) - integer_from_coord(origin.y);
  return px * Integer{dx} + py * Integer{dy};
}

Fraction integer_fraction(Integer value) {
  return Fraction{std::move(value), Integer{1}};
}

std::string to_string(const Integer& value) {
  return value.str();
}

std::string to_string(const Fraction& value) {
  return value.num.str() + "/" + value.den.str();
}

std::string to_string(const Decimal& value) {
  return value.str(50, std::ios_base::fmtflags(0));
}

Decimal decimal_from_integer(const Integer& value) {
  return Decimal{value.str()};
}

}  // namespace mer
