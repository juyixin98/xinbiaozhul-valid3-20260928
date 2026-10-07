#include "test_framework.h"

#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"

using namespace minkowski;

TEST_CASE("minkowski:two axis aligned squares") {
    auto a = validate_and_normalize(
        {{0, 0}, {1, 0}, {1, 1}, {0, 1}}).value;
    auto b = validate_and_normalize(
        {{2, 3}, {3, 3}, {3, 4}, {2, 4}}).value;
    const auto sum = minkowski_sum(a, b);
    CHECK(sum.kind == ShapeKind::Polygon);
    CHECK_EQ(test::points_to_string(sum.vertices),
             "2,3;4,3;4,5;2,5");
}

TEST_CASE("minkowski:parallel same-direction edges merge") {
    auto a = validate_and_normalize({{0, 0}, {2, 0}, {2, 1}, {0, 1}}).value;
    auto b = validate_and_normalize({{0, 0}, {3, 0}, {3, 2}, {0, 2}}).value;
    const auto sum = minkowski_sum(a, b);
    CHECK_EQ(test::points_to_string(sum.vertices),
             "0,0;5,0;5,3;0,3");
}

TEST_CASE("minkowski:triangle plus point is translation") {
    auto triangle = validate_and_normalize(
        {{0, 0}, {2, 0}, {0, 3}}).value;
    const auto sum = minkowski_sum(triangle, make_point({4, -5}));
    CHECK_EQ(test::points_to_string(sum.vertices),
             "4,-5;6,-5;4,-2");
}

TEST_CASE("minkowski:segment plus perpendicular segment is square") {
    const auto horizontal = make_segment({0, 0}, {1, 0});
    const auto vertical = make_segment({0, 0}, {0, 1});
    const auto sum = minkowski_sum(horizontal, vertical);
    CHECK(sum.kind == ShapeKind::Polygon);
    CHECK_EQ(test::points_to_string(sum.vertices),
             "0,0;1,0;1,1;0,1");
}

TEST_CASE("minkowski:collinear segment sum remains segment") {
    const auto a = make_segment({0, 0}, {2, 0});
    const auto b = make_segment({1, 0}, {4, 0});
    const auto sum = minkowski_sum(a, b);
    CHECK(sum.kind == ShapeKind::Segment);
    CHECK_EQ(test::points_to_string(sum.vertices), "1,0;6,0");
}
