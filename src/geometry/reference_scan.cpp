#include "nearest/geometry/reference_scan.hpp"

#include "nearest/geometry/predicates.hpp"

#include <algorithm>

namespace nearest::reference {

QueryOutcome exhaustiveNearestProjection(
    const Polyline& polyline,
    const RationalPoint& query) {
    QueryOutcome result;
    std::vector<ProjectionCandidate> all;
    all.reserve(polyline.segmentCount());

    for (std::size_t i = 0U; i < polyline.segmentCount(); ++i) {
        all.push_back(projectToSegment(
            query,
            polyline.vertices[i],
            polyline.vertices[i + 1U],
            i));
    }

    if (!all.empty()) {
        Rational best = all.front().distance_squared;
        for (const auto& candidate : all) {
            if (candidate.distance_squared < best) {
                best = candidate.distance_squared;
            }
        }
        for (auto& candidate : all) {
            if (candidate.distance_squared == best) {
                result.candidates.push_back(std::move(candidate));
            }
        }
    }

    result.leaves_visited = polyline.segmentCount();
    result.nodes_visited = polyline.segmentCount();
    return result;
}

}  // namespace nearest::reference
