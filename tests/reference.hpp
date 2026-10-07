#pragma once

// Independent reference implementations used ONLY by the tests.
// They deliberately share no code with the core algorithms:
//   - Minkowski sum by O(n*m) vertex-pair enumeration + Andrew monotone
//     chain convex hull (exact rational arithmetic).
//   - Support value by brute-force scan over all enumerated points.
// The core library never calls into this header.

#include <algorithm>
#include <vector>

#include "geom/point.hpp"

namespace ref {

inline bool less_xy(const geom::Point& a, const geom::Point& b) {
  return a.x != b.x ? a.x < b.x : a.y < b.y;
}

// Andrew monotone chain; returns the strict CCW hull (no collinear points).
inline std::vector<geom::Point> convex_hull(std::vector<geom::Point> p) {
  std::sort(p.begin(), p.end(), less_xy);
  p.erase(std::unique(p.begin(), p.end(),
                      [](const geom::Point& a, const geom::Point& b) {
                        return a == b;
                      }),
          p.end());
  const size_t n = p.size();
  if (n <= 1) return p;

  std::vector<geom::Point> h(2 * n);
  size_t k = 0;
  for (size_t i = 0; i < n; ++i) {
    while (k >= 2 && geom::cross(h[k - 2], h[k - 1], p[i]) <= 0) --k;
    h[k++] = p[i];
  }
  const size_t lower = k + 1;
  for (size_t i = n - 1; i-- > 0;) {
    while (k >= lower && geom::cross(h[k - 2], h[k - 1], p[i]) <= 0) --k;
    h[k++] = p[i];
  }
  h.resize(k - 1);
  return h;
}

// Reference Minkowski sum: enumerate all vertex sums, then re-hull.
inline std::vector<geom::Point> minkowski_enumerate(
    const std::vector<geom::Point>& a, const std::vector<geom::Point>& b) {
  std::vector<geom::Point> sums;
  sums.reserve(a.size() * b.size());
  for (const auto& p : a)
    for (const auto& q : b) sums.push_back(p + q);
  return convex_hull(std::move(sums));
}

// Reference support value: brute-force maximum over explicit points.
inline geom::cpp_rational support_brute(const std::vector<geom::Point>& pts,
                                        const geom::Point& d) {
  geom::cpp_rational best = geom::dot(pts.front(), d);
  for (const auto& p : pts)
    if (geom::dot(p, d) > best) best = geom::dot(p, d);
  return best;
}

}  // namespace ref
