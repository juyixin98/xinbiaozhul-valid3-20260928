#include "minkowski/convex_hull.h"

#include "minkowski/convex_set.h"
#include "minkowski/predicates.h"

#include <algorithm>

namespace minkowski {

ConvexSet convex_hull_from_points(std::vector<Point> points) {
    std::sort(points.begin(), points.end(), lex_less);
    points.erase(std::unique(points.begin(), points.end()), points.end());

    if (points.empty()) return {};
    if (points.size() == 1) return make_point(points.front());
    if (points.size() == 2) return make_segment(points[0], points[1]);

    std::vector<Point> lower;
    for (const Point& p : points) {
        while (lower.size() >= 2 &&
               cross(lower[lower.size() - 2], lower.back(), p) <= 0) {
            lower.pop_back();
        }
        lower.push_back(p);
    }

    std::vector<Point> upper;
    for (auto it = points.rbegin(); it != points.rend(); ++it) {
        const Point& p = *it;
        while (upper.size() >= 2 &&
               cross(upper[upper.size() - 2], upper.back(), p) <= 0) {
            upper.pop_back();
        }
        upper.push_back(p);
    }

    lower.pop_back();
    upper.pop_back();
    lower.insert(lower.end(), upper.begin(), upper.end());

    if (lower.size() == 2) {
        return make_segment(lower[0], lower[1]);
    }

    const auto it = std::min_element(lower.begin(), lower.end(), lex_less);
    std::rotate(lower.begin(), it, lower.end());
    return {ShapeKind::Polygon, std::move(lower)};
}

} // namespace minkowski
