#include "test_framework.h"

#include "minkowski/convex_hull.h"
#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"
#include "minkowski/support.h"

using namespace minkowski;

TEST_CASE("degeneracy:single point is separate kind") {
    const auto result = validate_and_normalize({{4, -2}});
    CHECK(result.ok);
    CHECK(result.value.kind == ShapeKind::Point);
    CHECK_EQ(test::points_to_string(result.value.vertices), "4,-2");
}

TEST_CASE("degeneracy:all collinear polygon points become segment") {
    const auto result = validate_and_normalize(
        {{3, 3}, {0, 0}, {1, 1}, {2, 2}});
    CHECK(result.ok);
    CHECK(result.value.kind == ShapeKind::Segment);
    CHECK_EQ(test::points_to_string(result.value.vertices), "0,0;3,3");
}

TEST_CASE("degeneracy:point plus point") {
    const auto sum = minkowski_sum(make_point({1, 2}),
                                   make_point({-4, 7}));
    CHECK(sum.kind == ShapeKind::Point);
    CHECK_EQ(test::points_to_string(sum.vertices), "-3,9");
}

TEST_CASE("degeneracy:point plus segment translates endpoints") {
    const auto sum = minkowski_sum(make_point({2, 2}),
                                   make_segment({0, 0}, {1, -3}));
    CHECK(sum.kind == ShapeKind::Segment);
    CHECK_EQ(test::points_to_string(sum.vertices), "2,-1;3,2");
}

TEST_CASE("degeneracy:empty sum is empty") {
    ConvexSet empty;
    const auto sum = minkowski_sum(empty, make_point({1, 1}));
    CHECK(sum.kind == ShapeKind::Empty);
    CHECK(sum.vertices.empty());
    CHECK_THROWS(support(empty, {1, 0}));
}
