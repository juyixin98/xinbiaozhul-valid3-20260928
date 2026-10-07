#pragma once

// ConvexPolygon: normalized, validated convex polygon with exact arithmetic.
//
// Normalization contract (applied by from_points):
//   1. Consecutive duplicate vertices are removed.
//   2. Winding is unified to counter-clockwise (CCW).
//   3. Collinear consecutive edges are merged (middle vertices dropped).
//   4. Strict convexity is validated; violations throw NonConvexError.
//   5. Degenerate inputs collapse to ShapeKind::Point (1 vertex) or
//      ShapeKind::Segment (2 vertices, all input points collinear).

#include <algorithm>
#include <stdexcept>
#include <utility>
#include <vector>

#include "geom/point.hpp"

namespace geom {

enum class ShapeKind { Point, Segment, Polygon };

inline const char* to_string(ShapeKind k) {
  switch (k) {
    case ShapeKind::Point: return "point";
    case ShapeKind::Segment: return "segment";
    case ShapeKind::Polygon: return "polygon";
  }
  return "unknown";
}

class GeometryError : public std::runtime_error {
 public:
  using std::runtime_error::runtime_error;
};

class NonConvexError : public GeometryError {
 public:
  using GeometryError::GeometryError;
};

class ConvexPolygon {
 public:
  static ConvexPolygon from_points(std::vector<Point> pts) {
    if (pts.empty()) throw GeometryError("empty vertex list");

    // 1. Drop consecutive duplicates (including the wrap-around pair).
    std::vector<Point> p;
    p.reserve(pts.size());
    for (const auto& q : pts)
      if (p.empty() || !(q == p.back())) p.push_back(q);
    while (p.size() > 1 && p.front() == p.back()) p.pop_back();

    if (p.size() == 1) return ConvexPolygon(std::move(p), ShapeKind::Point);

    // 2. Signed area decides degeneracy and winding.
    cpp_rational area2 = 0;
    const size_t n = p.size();
    for (size_t i = 0; i < n; ++i) area2 += cross(p[i], p[(i + 1) % n]);

    if (area2 == 0) {
      // All vertices collinear: keep only the two lexicographic extremes.
      auto less_xy = [](const Point& a, const Point& b) {
        return a.x != b.x ? a.x < b.x : a.y < b.y;
      };
      Point mn = p[0], mx = p[0];
      for (const auto& q : p) {
        if (less_xy(q, mn)) mn = q;
        if (less_xy(mx, q)) mx = q;
      }
      if (mn == mx) return ConvexPolygon({mn}, ShapeKind::Point);
      return ConvexPolygon({mn, mx}, ShapeKind::Segment);
    }

    // 3. Unify winding to CCW.
    if (area2 < 0) std::reverse(p.begin(), p.end());

    // 4+5. Merge collinear edges and validate strict convexity.
    std::vector<Point> m;
    m.reserve(p.size());
    for (size_t i = 0; i < n; ++i) {
      const Point& prev = p[(i + n - 1) % n];
      const Point& cur = p[i];
      const Point& next = p[(i + 1) % n];
      const cpp_rational c = cross(cur - prev, next - cur);
      if (c < 0)
        throw NonConvexError("reflex or clockwise turn at vertex " +
                             to_string(cur));
      if (c == 0) {
        if (dot(cur - prev, next - cur) > 0) continue;  // collinear middle
        throw NonConvexError("self-reversing (spike) edge at vertex " +
                             to_string(cur));
      }
      m.push_back(cur);
    }
    if (m.size() < 3)
      throw GeometryError("polygon collapsed below 3 vertices after merge");
    return ConvexPolygon(std::move(m), ShapeKind::Polygon);
  }

  ShapeKind kind() const noexcept { return kind_; }
  const std::vector<Point>& vertices() const noexcept { return v_; }
  size_t size() const noexcept { return v_.size(); }

  // Edge vector leaving vertex i (cyclic, CCW for polygons).
  Point edge(size_t i) const { return v_[(i + 1) % v_.size()] - v_[i]; }

  // Index of the lowest vertex by (y, then x); the canonical cycle start.
  size_t lowest_index() const {
    size_t best = 0;
    for (size_t i = 1; i < v_.size(); ++i) {
      const Point& q = v_[i];
      const Point& b = v_[best];
      if (q.y < b.y || (q.y == b.y && q.x < b.x)) best = i;
    }
    return best;
  }

 private:
  ConvexPolygon(std::vector<Point> v, ShapeKind k)
      : v_(std::move(v)), kind_(k) {}

  std::vector<Point> v_;
  ShapeKind kind_;
};

}  // namespace geom
