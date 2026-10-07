#include "nearest/geometry/decimal.hpp"
#include "nearest/geometry/polyline.hpp"

#include "nearest/geometry/predicates.hpp"

namespace nearest {

bool Polyline::hasZeroLengthSegment() const {
    for (std::size_t i = 1U; i < vertices.size(); ++i) {
        if (vertices[i - 1U].x == vertices[i].x &&
            vertices[i - 1U].y == vertices[i].y) {
            return true;
        }
    }
    return false;
}

void Polyline::ensureLengthCache() const {
    if (length_cache_ready) {
        return;
    }

    segment_lengths.clear();
    cumulative_lengths.clear();
    segment_lengths.reserve(segmentCount());
    cumulative_lengths.reserve(vertices.size());

    Decimal cumulative = 0;
    cumulative_lengths.push_back(cumulative);
    for (std::size_t i = 0U; i < segmentCount(); ++i) {
        RationalPoint edge = subtract(vertices[i + 1U], vertices[i]);
        Rational squared = edge.x * edge.x + edge.y * edge.y;
        Decimal length = boost::multiprecision::sqrt(rationalToDecimal(squared));
        segment_lengths.push_back(length);
        cumulative += length;
        cumulative_lengths.push_back(cumulative);
    }
    length_cache_ready = true;
}

Decimal Polyline::segmentLength(std::size_t segment_index) const {
    ensureLengthCache();
    return segment_lengths[segment_index];
}

Decimal Polyline::cumulativeLengthAtVertex(std::size_t vertex_index) const {
    ensureLengthCache();
    return cumulative_lengths[vertex_index];
}

std::optional<MileageResult> mileageForCandidate(
    const Polyline& polyline,
    const ProjectionCandidate& candidate) {
    if (candidate.segment_index >= polyline.segmentCount()) {
        return std::nullopt;
    }

    const std::size_t i = candidate.segment_index;
    Decimal start_mileage = polyline.cumulativeLengthAtVertex(i);
    Decimal length = polyline.segmentLength(i);

    // On a retained zero-length segment, t is normalized to zero and its
    // projection has the mileage shared by both coincident endpoints.
    return MileageResult{
        start_mileage,
        start_mileage + length * rationalToDecimal(candidate.t),
        length,
        rationalToDecimal(candidate.t)
    };
}

}  // namespace nearest
