#pragma once

// Minkowski sum of two convex shapes via cyclic edge merging, O(n + m).
//
// The edge sequence of each operand (walked CCW from its lowest vertex) is
// sorted by polar angle; merging the two sequences by angle and accumulating
// the steps from (lowest_A + lowest_B) yields the boundary of A (+) B.
// Degenerate shapes plug into the same scheme:
//   point   -> empty edge cycle (pure translation)
//   segment -> edge cycle [d, -d]
// Parallel edges from both operands are combined into a single step, so the
// raw result may contain collinear runs; ConvexPolygon::from_points performs
// the final collinear merge and degenerate-case reduction.

#include <vector>

#include "geom/polygon.hpp"

namespace geom {

// Polar-angle order starting from the +x axis, CCW.
inline bool angle_less(const Point& a, const Point& b) {
  const int ha = (a.y > 0 || (a.y == 0 && a.x > 0)) ? 0 : 1;
  const int hb = (b.y > 0 || (b.y == 0 && b.x > 0)) ? 0 : 1;
  if (ha != hb) return ha < hb;
  return cross(a, b) > 0;
}

// Edge vectors in CCW angular order, starting from the lowest vertex.
inline std::vector<Point> edge_cycle(const ConvexPolygon& P) {
  std::vector<Point> e;
  if (P.kind() == ShapeKind::Point) return e;
  const size_t s = P.lowest_index();
  const size_t n = P.size();
  e.reserve(n);
  for (size_t k = 0; k < n; ++k) e.push_back(P.edge((s + k) % n));
  return e;
}

inline ConvexPolygon minkowski_sum(const ConvexPolygon& A,
                                   const ConvexPolygon& B) {
  if (A.kind() == ShapeKind::Point && B.kind() == ShapeKind::Point)
    return ConvexPolygon::from_points({A.vertices()[0] + B.vertices()[0]});

  const std::vector<Point> ea = edge_cycle(A);
  const std::vector<Point> eb = edge_cycle(B);
  const Point start =
      A.vertices()[A.lowest_index()] + B.vertices()[B.lowest_index()];

  std::vector<Point> pts;
  pts.reserve(ea.size() + eb.size() + 1);
  pts.push_back(start);

  size_t i = 0, j = 0;
  while (i < ea.size() && j < eb.size()) {
    const cpp_rational c = cross(ea[i], eb[j]);
    if (c == 0 && dot(ea[i], eb[j]) > 0) {
      // Same direction: the sum boundary uses the combined edge.
      pts.push_back(pts.back() + (ea[i] + eb[j]));
      ++i;
      ++j;
    } else if (angle_less(ea[i], eb[j])) {
      pts.push_back(pts.back() + ea[i]);
      ++i;
    } else {
      pts.push_back(pts.back() + eb[j]);
      ++j;
    }
  }
  while (i < ea.size()) pts.push_back(pts.back() + ea[i++]);
  while (j < eb.size()) pts.push_back(pts.back() + eb[j++]);

  // The walk closes exactly (rationals): drop the repeated start point.
  pts.pop_back();
  return ConvexPolygon::from_points(std::move(pts));
}

}  // namespace geom
