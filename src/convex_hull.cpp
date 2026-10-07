#include "mbr/convex_hull.hpp"

#include "mbr/predicates.hpp"

#include <algorithm>

namespace mbr {
namespace {

std::vector<HullPoint> make_strict_half(
    const std::vector<HullPoint>& sorted,
    bool lower) {
    std::vector<HullPoint> half;
    for (const HullPoint& point : sorted) {
        while (half.size() >= 2) {
            Orientation turn = orientation(
                half[half.size() - 2].coordinate,
                half.back().coordinate,
                point.coordinate);
            bool remove = lower
                ? turn != Orientation::Counterclockwise
                : turn != Orientation::Counterclockwise;
            if (!remove) {
                break;
            }
            half.pop_back();
        }
        half.push_back(point);
    }
    return half;
}

}  // namespace

HullBuild build_convex_hull(std::vector<HullPoint> points) {
    std::sort(points.begin(), points.end(),
              [](const HullPoint& a, const HullPoint& b) {
                  return a.coordinate < b.coordinate;
              });

    std::vector<HullPoint> unique;
    for (HullPoint& point : points) {
        if (!unique.empty() &&
            unique.back().coordinate == point.coordinate) {
            for (std::string& id : point.source_ids) {
                unique.back().source_ids.push_back(std::move(id));
            }
            continue;
        }
        unique.push_back(std::move(point));
    }

    HullBuild result;
    result.unique_points = unique;
    if (unique.size() <= 1) {
        result.hull = unique;
        return result;
    }

    std::vector<HullPoint> lower = make_strict_half(unique, true);
    std::vector<HullPoint> reversed(unique.rbegin(), unique.rend());
    std::vector<HullPoint> upper = make_strict_half(reversed, false);

    lower.pop_back();
    upper.pop_back();
    lower.insert(lower.end(),
                 std::make_move_iterator(upper.begin()),
                 std::make_move_iterator(upper.end()));
    result.hull = std::move(lower);
    return result;
}

}  // namespace mbr
