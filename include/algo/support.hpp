#pragma once

// Support-direction queries on convex shapes:
//   h_P(d) = max_{p in P} <p, d>          (support value)
//   argmax vertex for direction d         (support point)
//
// Polygons are answered in O(log n): the predicate g(i) = <d, edge_i> >= 0
// is a cyclic shift of (+...+ -...-), so at most two monotone binary
// searches locate the sign transition, i.e. the maximizing vertex.
// Points and segments are answered by direct scan.

#include <cstddef>

#include "geom/polygon.hpp"

namespace geom {

inline size_t support_index(const ConvexPolygon& P, const Point& d) {
  const auto& v = P.vertices();
  const size_t n = v.size();
  if (n <= 2 || (d.x == 0 && d.y == 0)) {
    size_t best = 0;
    for (size_t i = 1; i < n; ++i)
      if (dot(v[i], d) > dot(v[best], d)) best = i;
    return best;
  }

  auto g = [&](size_t i) { return dot(d, P.edge(i)) >= 0; };

  // First index in [lo, hi) where g differs from g(lo); hi if none.
  // Valid because g is constant on a prefix of any searched interval.
  auto first_change = [&](size_t lo, size_t hi) {
    const bool base = g(lo);
    size_t l = lo, h = hi;
    while (h - l > 1) {
      const size_t m = (l + h) / 2;
      if (g(m) == base) l = m; else h = m;
    }
    return h;
  };

  if (g(0)) {
    // Pattern +...+ -...- : the first negative edge starts at the peak.
    const size_t t = first_change(0, n);
    return t % n;  // t == n only for d == 0 (handled above)
  }
  // Pattern -...- +...+ -...- : skip the leading negative arc, then find
  // the end of the positive arc.
  const size_t t1 = first_change(0, n);
  const size_t t2 = first_change(t1, n);
  return t2 % n;
}

inline cpp_rational support_value(const ConvexPolygon& P, const Point& d) {
  return dot(P.vertices()[support_index(P, d)], d);
}

inline Point support_point(const ConvexPolygon& P, const Point& d) {
  return P.vertices()[support_index(P, d)];
}

}  // namespace geom
