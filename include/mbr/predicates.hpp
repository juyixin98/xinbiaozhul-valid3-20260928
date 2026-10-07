#pragma once

#include "mbr/geometry.hpp"

namespace mbr {

enum class Orientation {
    Counterclockwise,
    Clockwise,
    Collinear
};

Orientation orientation(const Point& a, const Point& b, const Point& c);

struct NearCollinearResult {
    bool near_collinear = false;
    bool reference_segment_is_zero_length = false;
    Rational squared_cross;
    Rational squared_reference_length;
};

NearCollinearResult near_collinear(const Point& a, const Point& b,
                                   const Point& c, const Rational& tolerance);

}  // namespace mbr
