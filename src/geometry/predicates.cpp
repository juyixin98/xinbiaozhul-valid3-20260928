#include "nearest/geometry/predicates.hpp"

namespace nearest {

Rational exactDistanceSquared(const RationalPoint& a, const RationalPoint& b) {
    Rational dx = a.x - b.x;
    Rational dy = a.y - b.y;
    return dx * dx + dy * dy;
}

Rational dot(const RationalPoint& a, const RationalPoint& b) {
    return a.x * b.x + a.y * b.y;
}

Rational cross(const RationalPoint& a, const RationalPoint& b) {
    return a.x * b.y - a.y * b.x;
}

RationalPoint subtract(const RationalPoint& a, const RationalPoint& b) {
    return {a.x - b.x, a.y - b.y};
}

int exactSign(const Rational& value) {
    if (value == 0) {
        return 0;
    }
    return value > 0 ? 1 : -1;
}

ProjectionCandidate projectToSegment(
    const RationalPoint& query,
    const RationalPoint& start,
    const RationalPoint& end,
    std::size_t segment_index) {
    RationalPoint edge = subtract(end, start);
    Rational length_squared = edge.x * edge.x + edge.y * edge.y;
    Rational parameter = 0;

    if (length_squared != 0) {
        Rational along = dot(subtract(query, start), edge);
        parameter = along / length_squared;
        if (parameter < 0) {
            parameter = 0;
        } else if (parameter > 1) {
            parameter = 1;
        }
    }

    RationalPoint projected{
        start.x + parameter * edge.x,
        start.y + parameter * edge.y
    };

    return {
        segment_index,
        projected,
        parameter,
        exactDistanceSquared(query, projected)
    };
}

}  // namespace nearest
