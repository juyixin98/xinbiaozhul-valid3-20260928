#include "mbr/predicates.hpp"
#include "test_support.hpp"

#include <string>

int main() {
    using namespace mbr;
    Point a{{0}, {0}};
    Point b{{2}, {0}};
    Point ccw{{1}, {1}};
    Point cw{{1}, {-1}};
    Point line{{1}, {0}};

    CHECK(orientation(a, b, ccw) == Orientation::Counterclockwise);
    CHECK(orientation(a, b, cw) == Orientation::Clockwise);
    CHECK(orientation(a, b, line) == Orientation::Collinear);

    auto exact = near_collinear(a, b, Point{Rational(1), Rational(1, 1000)},
                                Rational(1, 100));
    CHECK(!exact.reference_segment_is_zero_length);
    CHECK(exact.near_collinear);

    auto outside = near_collinear(a, b, Point{Rational(1), Rational(1, 50)},
                                  Rational(1, 100));
    CHECK(!outside.near_collinear);

    auto zero_reference = near_collinear(a, a, ccw, Rational(1, 10));
    CHECK(zero_reference.reference_segment_is_zero_length);
    CHECK(!zero_reference.near_collinear);
    CHECK(zero_reference.squared_reference_length == Rational(0));
    return 0;
}
