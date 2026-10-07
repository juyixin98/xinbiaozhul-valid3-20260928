#include "test_framework.h"

#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"
#include "minkowski/support.h"
#include "minkowski/verification.h"

using namespace minkowski;

TEST_CASE("support:unique integer maximizing vertex") {
    const auto square = validate_and_normalize(
        {{0, 0}, {1, 0}, {1, 1}, {0, 1}}).value;
    const auto result = support(square, {2, 3});
    CHECK_EQ(result.value, Integer{5});
    CHECK_EQ(test::points_to_string(result.maximizers), "1,1");
}

TEST_CASE("support:edge has all tied maximizing endpoints") {
    const auto square = validate_and_normalize(
        {{0, 0}, {1, 0}, {1, 1}, {0, 1}}).value;
    const auto result = support(square, {1, 0});
    CHECK_EQ(result.value, Integer{1});
    CHECK_EQ(test::points_to_string(result.maximizers), "1,0;1,1");
}

TEST_CASE("support:zero direction returns every represented vertex") {
    const auto triangle = validate_and_normalize(
        {{0, 0}, {3, 0}, {0, 4}}).value;
    const auto result = support(triangle, {0, 0});
    CHECK_EQ(result.value, Integer{0});
    CHECK(result.maximizers.size() == 3);
}

TEST_CASE("support:additivity against explicit Minkowski square") {
    const auto a = make_segment({0, 0}, {2, 0});
    const auto b = make_segment({0, 0}, {0, 3});
    const auto report = verify_support_additivity(a, b, {5, -7});
    CHECK(report.passed);
    CHECK_EQ(support(minkowski_sum(a, b), {5, -7}).value, Integer{10});
}
