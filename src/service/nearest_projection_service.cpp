#include "nearest/service/nearest_projection_service.hpp"

#include "nearest/geometry/decimal.hpp"
#include "nearest/geometry/polyline.hpp"

#include <iomanip>

namespace nearest::service {

NearestProjectionService::NearestProjectionService(
    PolylineProjector projector,
    Configuration configuration)
    : projector_(std::move(projector)), configuration_(configuration) {}

RequestResult NearestProjectionService::process(const QueryRequest& request) const {
    RequestResult result;
    result.request = request;

    if (!projector_.ready()) {
        result.decision = Decision::Indeterminate;
        result.category = FailureCategory::InternalError;
        result.message = "projector is not initialized";
        return result;
    }

    QueryOutcome outcome = projector_.query(request.point);
    result.nodes_visited = outcome.nodes_visited;
    result.nodes_pruned = outcome.nodes_pruned;
    result.leaves_visited = outcome.leaves_visited;

    if (outcome.candidates.empty()) {
        result.decision = Decision::Indeterminate;
        result.category = FailureCategory::InternalError;
        result.message = "spatial index returned no candidate";
        return result;
    }

    for (const ProjectionCandidate& candidate : outcome.candidates) {
        std::optional<MileageResult> mileage = mileageForCandidate(
            projector_.polyline(), candidate);
        if (!mileage.has_value()) {
            result.decision = Decision::Indeterminate;
            result.category = FailureCategory::InternalError;
            result.message = "candidate segment index has no mileage";
            return result;
        }
        result.candidates.push_back({candidate, *mileage});
    }

    result.decision = Decision::Accepted;
    result.message = "accepted " + std::to_string(result.candidates.size()) +
        " exact optimal candidate(s)";
    return result;
}

void NearestProjectionService::emitResult(
    std::ostream& output,
    const RequestResult& result) const {
    if (result.candidates.empty()) {
        output << result.request.request_id << ','
               << decisionName(result.decision) << ','
               << failureName(result.category)
               << ",0,0,,,,,,,,,,"
               << result.nodes_visited << ','
               << result.nodes_pruned << ','
               << result.leaves_visited << '\n';
        return;
    }

    for (std::size_t i = 0U; i < result.candidates.size(); ++i) {
        const LocatedCandidate& located = result.candidates[i];
        output << result.request.request_id << ','
               << decisionName(result.decision) << ','
               << failureName(result.category) << ','
               << (i + 1U) << ',' << result.candidates.size() << ','
               << located.projection.segment_index << ','
               << formatDecimal(rationalToDecimal(located.projection.t)) << ','
               << formatDecimal(rationalToDecimal(located.projection.point.x)) << ','
               << formatDecimal(rationalToDecimal(located.projection.point.y)) << ','
               << formatDecimal(rationalToDecimal(located.projection.distance_squared))
               << ','
               << formatDecimal(located.mileage.start_mileage) << ','
               << formatDecimal(located.mileage.projected_mileage) << ','
               << formatDecimal(located.mileage.local_segment_length) << ','
               << result.nodes_visited << ','
               << result.nodes_pruned << ','
               << result.leaves_visited << '\n';
    }
}

}  // namespace nearest::service
