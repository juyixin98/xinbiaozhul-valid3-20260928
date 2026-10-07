#pragma once

#include "mbr/convex_hull.hpp"
#include "mbr/solver.hpp"

namespace mbr {

RectangleSolution minimum_bounding_rectangle(
    std::string run_id,
    std::size_t input_point_count,
    HullBuild hull_build,
    const SolverOptions& options);

}  // namespace mbr
