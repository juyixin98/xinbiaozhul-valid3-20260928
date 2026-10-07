#include "test_framework.h"

#include "minkowski/predicates.h"

using namespace minkowski;

TEST_CASE("predicates:orientation signs") {
    CHECK(orientation({0, 0}, {1, 0}, {1, 1}) ==
          Orientation::CounterClockwise);
    CHECK(orientation({0, 0}, {0, 1}, {1, 1}) == Orientation::Clockwise);
    CHECK(orientation({0, 0}, {2, 0}, {5, 0}) == Orientation::Collinear);
}

TEST_CASE("predicates:cyclic edge angle order") {
    const Point east{1, 0};
    const Point north{0, 1};
    const Point west{-1, 0};
    const Point south{0, -1};
    CHECK(edge_angle_less(east, north));
    CHECK(edge_angle_less(north, west));
    CHECK(edge_angle_less(west, south));
    CHECK(same_direction({2, 0}, {3, 0}));
    CHECK(!same_direction({2, 0}, {-3, 0}));
    CHECK(!same_direction({1, 1}, {1, 0}));
}

TEST_CASE("predicates:dot cross and lex order") {
    CHECK_EQ(minkowski::Integer(dot({2, -3}, {4, 5})), Integer{-7});
    CHECK_EQ(minkowski::Integer(cross({2, 0}, {0, 3})), Integer{6});
    CHECK(lex_less({1, 9}, {2, -100}));
    CHECK(lex_less({1, -100}, {1, 9}));
}
