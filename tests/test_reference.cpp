#include "test_framework.h"

#include "minkowski/convex_hull.h"
#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"
#include "minkowski/verification.h"

using namespace minkowski;

TEST_CASE("reference:monotone hull removes interior point") {
    // Points include an interior point and out-of-order duplicate values.
    const auto hull = convex_hull_from_points({
        {0, 0}, {2, 0}, {2, 2}, {0, 2}, {1, 1},
        {0, 0}, {2, 1}, {1, 0}
    });
    CHECK(hull.kind == ShapeKind::Polygon);
    CHECK_EQ(test::points_to_string(hull.vertices),
             "0,0;2,0;2,2;0,2");
}

TEST_CASE("reference:pairwise enumeration validates edge merge triangles") {
    const auto a = validate_and_normalize(
        {{0, 0}, {2, 0}, {0, 2}}).value;
    const auto b = validate_and_normalize(
        {{0, 0}, {1, 0}, {0, 1}}).value;
    const auto sum = minkowski_sum(a, b);
    const auto report = verify_minkowski(sum, a, b);
    CHECK(report.passed);
    CHECK(report.expected.kind == ShapeKind::Polygon);
    CHECK_EQ(test::points_to_string(report.expected.vertices),
             "0,0;3,0;0,3");
}

TEST_CASE("reference:translation check uses explicit canonical answer") {
    const auto shape = validate_and_normalize(
        {{1, 1}, {4, 1}, {1, 5}}).value;
    const auto moved = translate(shape, {-2, 3});
    const auto report = verify_translation(moved, shape, {-2, 3});
    CHECK(report.passed);
    CHECK_EQ(test::points_to_string(report.expected.vertices),
             "-1,4;2,4;-1,8");
}

TEST_CASE("reference:degenerate sum independently enumerated") {
    const auto a = make_segment({0, 0}, {2, 0});
    const auto b = make_segment({1, 0}, {3, 0});
    const auto sum = minkowski_sum(a, b);
    const auto reference = reference_pairwise_sum(a, b);
    CHECK(canonically_equal(sum, reference));
    CHECK(reference.kind == ShapeKind::Segment);
    CHECK_EQ(test::points_to_string(reference.vertices), "1,0;5,0");
}
