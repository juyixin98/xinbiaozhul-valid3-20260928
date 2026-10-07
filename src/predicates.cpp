#include "minkowski/predicates.h"

namespace minkowski {

Orientation orientation(const Point& a, const Point& b, const Point& c) {
    const Integer value = cross(a, b, c);
    if (value > 0) return Orientation::CounterClockwise;
    if (value < 0) return Orientation::Clockwise;
    return Orientation::Collinear;
}

bool edge_angle_less(const Point& a, const Point& b) {
    const auto half = [](const Point& p) {
        return (p.y > 0 || (p.y == 0 && p.x > 0)) ? 0 : 1;
    };
    const int ha = half(a);
    const int hb = half(b);
    if (ha != hb) return ha < hb;
    return cross(a, b) > 0;
}

bool same_direction(const Point& a, const Point& b) {
    return cross(a, b) == 0 && dot(a, b) > 0;
}

bool lex_less(const Point& a, const Point& b) {
    if (a.x != b.x) return a.x < b.x;
    return a.y < b.y;
}

Integer manhattan_norm(const Point& p) {
    Integer ax = p.x;
    Integer ay = p.y;
    if (ax < 0) ax = -ax;
    if (ay < 0) ay = -ay;
    return ax + ay;
}

} // namespace minkowski
