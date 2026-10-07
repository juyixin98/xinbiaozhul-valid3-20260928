#pragma once

#include "mer/hull.hpp"
#include "mer/types.hpp"

#include <vector>

namespace mer {

struct AlgorithmOptions {
  // Keeps exhaustive replay evidence for every hull-edge direction.
  bool retain_edge_log = true;
};

// Minimum-area enclosing rectangle. The input must be the original point set:
// contacts retain every original ID, including duplicate coordinate IDs.
MinimumRectangle minimum_area_rectangle(const std::vector<Point>& points,
                                       const AlgorithmOptions& options = {});

}  // namespace mer
