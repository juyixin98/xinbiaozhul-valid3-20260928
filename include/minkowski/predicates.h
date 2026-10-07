#pragma once

#include "minkowski/types.h"

namespace minkowski {

enum class Orientation {
    Clockwise = -1,
    Collinear = 0,
    CounterClockwise = 1
};

Orientation orientation(const Point& a, const Point& b, const Point& c);

// Half-plane ordering for convex polygon edges: upper half first, then lower
// half. This is the same cyclic angle order used after normalization.
bool edge_angle_less(const Point& a, const Point& b);

bool same_direction(const Point& a, const Point& b);
bool lex_less(const Point& a, const Point& b);

Integer manhattan_norm(const Point& p);

} // namespace minkowski
