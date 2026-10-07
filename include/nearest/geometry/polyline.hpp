#pragma once

#include "nearest/geometry/types.hpp"

#include <cstddef>
#include <optional>
#include <string>
#include <vector>

namespace nearest {

struct Polyline {
    std::vector<RationalPoint> vertices;
    mutable std::vector<Decimal> segment_lengths;
    mutable std::vector<Decimal> cumulative_lengths;
    mutable bool length_cache_ready = false;

    [[nodiscard]] std::size_t segmentCount() const noexcept {
        return vertices.size() <= 1U ? 0U : vertices.size() - 1U;
    }

    [[nodiscard]] bool hasZeroLengthSegment() const;
    [[nodiscard]] Decimal segmentLength(std::size_t segment_index) const;
    [[nodiscard]] Decimal cumulativeLengthAtVertex(std::size_t vertex_index) const;
    void ensureLengthCache() const;
};

struct MileageResult {
    Decimal start_mileage{};
    Decimal projected_mileage{};
    Decimal local_segment_length{};
    Decimal t{};
};

std::optional<MileageResult> mileageForCandidate(
    const Polyline& polyline,
    const ProjectionCandidate& candidate);

}  // namespace nearest
