#include "nearest/service/polyline_projector.hpp"

namespace nearest::service {

ProjectorBuildResult PolylineProjector::build(
    Polyline polyline,
    const Configuration& configuration) {
    ready_ = false;
    index_.reset();

    if (polyline.vertices.empty()) {
        return {FailureCategory::EmptyPolyline, "polyline contains no vertices"};
    }
    if (polyline.vertices.size() == 1U) {
        return {FailureCategory::InsufficientVertices,
            "polyline requires at least two vertices to define a segment"};
    }

    if (configuration.zero_length_policy == ZeroLengthPolicy::Reject &&
        polyline.hasZeroLengthSegment()) {
        return {FailureCategory::ZeroLengthRejectedByConfiguration,
            "configuration rejects zero-length segments"};
    }

    polyline_ = std::move(polyline);
    configuration_ = configuration;
    index_ = std::make_unique<spatial::BvhIndex>(polyline_);
    ready_ = true;
    return {};
}

QueryOutcome PolylineProjector::query(const RationalPoint& point) const {
    if (!ready_) {
        return {};
    }
    QueryOutcome outcome = index_->nearest(point);
    if (configuration_.tie_policy == TiePolicy::FirstCandidate &&
        !outcome.candidates.empty()) {
        outcome.candidates.resize(1U);
    }
    return outcome;
}

const Polyline& PolylineProjector::polyline() const noexcept {
    return polyline_;
}

const Configuration& PolylineProjector::configuration() const noexcept {
    return configuration_;
}

bool PolylineProjector::ready() const noexcept {
    return ready_;
}

}  // namespace nearest::service
