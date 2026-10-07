#pragma once

#include "minkowski/types.h"

#include <vector>

namespace minkowski {

// Independent monotone-chain reference. The input point set is not assumed to
// have an ordering. Duplicates are removed here because this function models a
// set-based reference computation.
ConvexSet convex_hull_from_points(std::vector<Point> points);

} // namespace minkowski
