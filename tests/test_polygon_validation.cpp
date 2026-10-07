#include "test_framework.h"

#include "minkowski/convex_set.h"

using namespace minkowski;

TEST_CASE("validation:clockwise square is reversed") {
    const auto result = validate_and_normalize(
        {{0, 0}, {0, 1}, {1, 1}, {1, 0}});
    CHECK(result.ok);
    CHECK(result.value.kind == ShapeKind::Polygon);
    CHECK_EQ(test::points_to_string(result.value.vertices),
             "0,0;1,0;1,1;0,1");
}

TEST_CASE("validation:collinear boundary points are merged") {
    const auto result = validate_and_normalize(
        {{0, 0}, {1, 0}, {2, 0}, {2, 2}, {0, 2}});
    CHECK(result.ok);
    CHECK(result.value.kind == ShapeKind::Polygon);
    CHECK_EQ(test::points_to_string(result.value.vertices),
             "0,0;2,0;2,2;0,2");
}

TEST_CASE("validation:duplicate has explicit category") {
    const auto result = validate_and_normalize(
        {{0, 0}, {1, 0}, {1, 1}, {1, 0}});
    CHECK(!result.ok);
    CHECK(result.error == ValidationError::DuplicateVertex);
}

TEST_CASE("validation:concave polygon has explicit category") {
    const auto result = validate_and_normalize(
        {{0, 0}, {3, 0}, {3, 3}, {2, 1}, {0, 3}});
    CHECK(!result.ok);
    CHECK(result.error == ValidationError::NonConvex);
}

TEST_CASE("validation:empty input has explicit category") {
    const auto result = validate_and_normalize({});
    CHECK(!result.ok);
    CHECK(result.error == ValidationError::EmptyInput);
}
