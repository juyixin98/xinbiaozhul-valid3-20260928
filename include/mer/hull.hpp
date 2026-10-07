#pragma once

#include "mer/types.hpp"

#include <vector>

namespace mer {

// Builds a strict convex hull. Duplicate coordinates are accepted; IDs of
// duplicates are retained by callers in contact sets, but hull vertices use
// the first occurrence ID. Empty and all-collinear inputs remain degenerate.
Hull convex_hull(const std::vector<Point>& points);

}  // namespace mer
