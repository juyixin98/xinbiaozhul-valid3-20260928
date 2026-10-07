#pragma once

#include "nearest/geometry/types.hpp"

namespace nearest {

Rational exactDistanceSquared(const RationalPoint& a, const RationalPoint& b);
Rational dot(const RationalPoint& a, const RationalPoint& b);
Rational cross(const RationalPoint& a, const RationalPoint& b);
RationalPoint subtract(const RationalPoint& a, const RationalPoint& b);
int exactSign(const Rational& value);

ProjectionCandidate projectToSegment(
    const RationalPoint& query,
    const RationalPoint& start,
    const RationalPoint& end,
    std::size_t segment_index);

}  // namespace nearest
