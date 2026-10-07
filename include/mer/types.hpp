#pragma once

#include <boost/multiprecision/cpp_dec_float.hpp>
#include <boost/multiprecision/cpp_int.hpp>

#include <array>
#include <cstdint>
#include <string>
#include <vector>

namespace mer {

using Coord = std::int64_t;
using Integer = boost::multiprecision::cpp_int;
using Decimal = boost::multiprecision::cpp_dec_float_100;

// Coordinates in the public input are required to fit in signed 64-bit.
struct Point {
  Coord x = 0;
  Coord y = 0;
  std::int64_t id = -1;

  bool operator==(const Point& other) const noexcept {
    return x == other.x && y == other.y;
  }
};

struct Fraction {
  Integer num;
  Integer den;  // Always strictly positive.

  Fraction() = default;
  Fraction(Integer n, Integer d);

  Decimal value() const;
  bool operator==(const Fraction& other) const;
};

struct ExactPoint {
  Fraction x;
  Fraction y;
};

struct RationalPoint {
  long double x = 0.0L;
  long double y = 0.0L;
};

enum class HullKind { Empty, Point, Segment, Polygon };

struct Hull {
  HullKind kind = HullKind::Empty;
  // Polygon vertices are counter-clockwise and do not contain collinear
  // interior vertices. Segment points are lexicographically ordered.
  std::vector<Point> vertices;
};

struct EdgeEvidence {
  std::size_t edge_index = 0;
  std::size_t edge_count = 0;
  Point edge_start;
  Point edge_end;

  Integer direction_x;
  Integer direction_y;
  Integer min_tangent_dot;
  Integer max_tangent_dot;
  Integer min_axis_dot;
  Integer max_axis_dot;

  // area = width_projection * height_projection / squared_edge_length.
  Integer area_num;
  Integer area_den;

  std::vector<std::int64_t> side_contact_ids;
  std::vector<std::int64_t> opposite_contact_ids;
  std::vector<std::int64_t> begin_contact_ids;
  std::vector<std::int64_t> end_contact_ids;

  bool selected = false;
  std::string decision;
};

struct MinimumRectangle {
  HullKind hull_kind = HullKind::Empty;
  std::array<ExactPoint, 4> corners{};
  // Counter-clockwise corners corresponding to corners[0..3].
  std::array<std::vector<std::int64_t>, 4> corner_contact_ids;

  // Point: area 0, both scale denominators one.
  // Segment: zero-height direction; perpendicular width denominator is one.
  Fraction area;
  Decimal area_decimal{0};
  Decimal area_error_bound{0};

  std::array<Decimal, 4> corner_error_x{};
  std::array<Decimal, 4> corner_error_y{};

  std::vector<EdgeEvidence> edge_log;
  std::size_t selected_edge_index = 0;
};

struct NearCollinearReport {
  bool near_collinear = false;
  Integer exact_cross;
  Integer squared_length;
  Decimal scaled_residual;
  Decimal tolerance;
};

Integer integer_from_coord(Coord value);
Integer cross(const Point& a, const Point& b, const Point& c);
Integer dot_delta(const Point& origin, const Point& p, Coord dx, Coord dy);
Fraction integer_fraction(Integer value);
std::string to_string(const Integer& value);
std::string to_string(const Fraction& value);
std::string to_string(const Decimal& value);
Decimal decimal_from_integer(const Integer& value);

}  // namespace mer
