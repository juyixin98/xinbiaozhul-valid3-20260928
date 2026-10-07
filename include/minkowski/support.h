#pragma once

#include "minkowski/types.h"

#include <vector>

namespace minkowski {

struct SupportResult {
    Integer value;
    std::vector<Point> maximizers;
};

SupportResult support(const ConvexSet& set, const Point& direction);

} // namespace minkowski
