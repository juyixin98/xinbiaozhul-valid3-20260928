#pragma once

#include "minkowski/types.h"

namespace minkowski {

ConvexSet minkowski_sum(const ConvexSet& a, const ConvexSet& b);
ConvexSet translate(const ConvexSet& set, const Point& offset);

} // namespace minkowski
