#include "mer/hull.hpp"

#include <algorithm>
#include <utility>

#include "mer/predicates.hpp"

namespace mer {

Hull convex_hull(const std::vector<Point>& points) {
  if (points.empty()) {
    return Hull{HullKind::Empty, {}};
  }

  std::vector<Point> unique_points;
  unique_points.reserve(points.size());
  for (const Point& p : points) {
    auto it = std::find(unique_points.begin(), unique_points.end(), p);
    if (it == unique_points.end()) {
      unique_points.push_back(p);
    }
  }

  std::sort(unique_points.begin(), unique_points.end(),
            [](const Point& a, const Point& b) {
              return std::tie(a.x, a.y) < std::tie(b.x, b.y);
            });

  if (unique_points.size() == 1) {
    return Hull{HullKind::Point, unique_points};
  }

  std::vector<Point> lower;
  for (const Point& p : unique_points) {
    while (lower.size() >= 2 &&
           exact_orientation(lower[lower.size() - 2], lower.back(), p) <= 0) {
      lower.pop_back();
    }
    lower.push_back(p);
  }

  std::vector<Point> upper;
  for (auto it = unique_points.rbegin(); it != unique_points.rend(); ++it) {
    const Point& p = *it;
    while (upper.size() >= 2 &&
           exact_orientation(upper[upper.size() - 2], upper.back(), p) <= 0) {
      upper.pop_back();
    }
    upper.push_back(p);
  }

  lower.pop_back();
  upper.pop_back();
  lower.insert(lower.end(), upper.begin(), upper.end());

  if (lower.size() < 3) {
    // Sorting is lexicographic, matching the contract for segment hulls.
    std::sort(lower.begin(), lower.end(), [](const Point& a, const Point& b) {
      return std::tie(a.x, a.y) < std::tie(b.x, b.y);
    });
    return Hull{HullKind::Segment, {lower.front(), lower.back()}};
  }

  return Hull{HullKind::Polygon, std::move(lower)};
}

}  // namespace mer
