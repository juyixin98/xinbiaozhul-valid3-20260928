#pragma once

#include "geometry/polyline.hpp"
#include "geometry/projection.hpp"

namespace mileage {

// Shared post-processing for any nearest-projection engine: keeps only the
// exact-minimum candidates, orders them stably by (segment index, t), and
// fills in mileage. Engines differ in how they enumerate segments, never in
// how ties are resolved.
QueryResult finalize_candidates(const Polyline& line,
                                std::vector<Candidate> best);

}  // namespace mileage
