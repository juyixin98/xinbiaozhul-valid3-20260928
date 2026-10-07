#pragma once

#include "mbr/geometry.hpp"

#include <utility>
#include <vector>

namespace mbr {

// Builds a strict counterclockwise convex hull. Coincident coordinates are
// aggregated into one hull/unique point with all source identifiers.
// Collinear non-corner points are omitted from hull vertices but remain in
// unique_points for projection and contact evidence.
struct HullBuild {
    std::vector<HullPoint> unique_points;
    std::vector<HullPoint> hull;
};

HullBuild build_convex_hull(std::vector<HullPoint> points);

}  // namespace mbr
