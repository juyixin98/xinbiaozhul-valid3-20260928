#pragma once

#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/types.hpp"

namespace nearest::reference {

// This deliberately scans every segment and is used only by tests/diagnostics.
// It does not call the BVH implementation being tested.
QueryOutcome exhaustiveNearestProjection(
    const Polyline& polyline,
    const RationalPoint& query);

}  // namespace nearest::reference
