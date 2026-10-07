#include "mbr/predicates.hpp"

namespace mbr {

Orientation orientation(const Point& a, const Point& b, const Point& c) {
    Rational value = cross(b - a, c - a);
    if (value.sign() > 0) return Orientation::Counterclockwise;
    if (value.sign() < 0) return Orientation::Clockwise;
    return Orientation::Collinear;
}

NearCollinearResult near_collinear(const Point& a, const Point& b,
                                   const Point& c, const Rational& tolerance) {
    if (tolerance.sign() < 0) {
        throw std::invalid_argument("near-collinear tolerance cannot be negative");
    }
    Point ab = b - a;
    Point ac = c - a;
    Rational squared_cross = cross(ab, ac).square();
    Rational length_squared = dot(ab, ab);

    NearCollinearResult result;
    result.squared_cross = squared_cross;
    result.squared_reference_length = length_squared;
    if (length_squared.sign() == 0) {
        result.reference_segment_is_zero_length = true;
        return result;
    }

    result.near_collinear =
        squared_cross <= tolerance.square() * length_squared;
    return result;
}

}  // namespace mbr
