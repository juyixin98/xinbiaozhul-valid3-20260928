#pragma once

#include <cstdint>

#include "geometry/polyline.hpp"
#include "geometry/projection.hpp"
#include "spatial/candidate_set.hpp"

namespace mileage {

struct ScanStats {
  uint64_t segments_examined = 0;
};

// Reference engine: examines every segment with exact arithmetic. This is the
// ground truth the accelerated index is validated against; it is deliberately
// a separate, simpler code path from the index.
QueryResult nearest_full_scan(const Polyline& line, const Vec2& q,
                              ScanStats* stats = nullptr);

}  // namespace mileage
