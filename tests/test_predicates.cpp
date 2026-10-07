#include "test_framework.hpp"

#include "nearest/geometry/decimal.hpp"
#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/predicates.hpp"
#include "nearest/service/configuration.hpp"
#include "nearest/service/polyline_projector.hpp"

using namespace nearest;

TEST_CASE(decimal_rational_parser_handles_negative_exponent) {
    auto value = parseDecimalRational("-0.25");
    testlib::check(value.has_value(), "parser accepted -0.25");
    testlib::check(*value == Rational(-1, 4), "value is exact -1/4");

    auto exponent = parseDecimalRational("1.2e2");
    testlib::check(exponent.has_value(), "parser accepted 1.2e2");
    testlib::check(*exponent == 120, "1.2e2 is exactly 120");

    testlib::check(!parseDecimalRational("1.x").has_value(), "rejects 1.x");
}

TEST_CASE(zero_length_segment_retains_shared_mileage) {
    Polyline polyline{{
        {0, 0},
        {0, 0},
        {3, 4}
    }};

    ProjectionCandidate candidate = projectToSegment({0, 1},
        polyline.vertices[0], polyline.vertices[1], 0);
    testlib::check(candidate.t == 0, "zero-length t is zero");
    testlib::check(candidate.point.x == 0 && candidate.point.y == 0,
        "zero-length projection is the retained endpoint");

    auto mileage = mileageForCandidate(polyline, candidate);
    testlib::check(mileage.has_value(), "zero segment has mileage result");
    testlib::check(mileage->projected_mileage == 0,
        "zero segment contributes zero mileage");
}

TEST_CASE(reject_policy_names_specific_failure_category) {
    Polyline polyline{{{0, 0}, {0, 0}, {1, 1}}};
    service::Configuration configuration;
    configuration.zero_length_policy = service::ZeroLengthPolicy::Reject;
    service::PolylineProjector projector;
    auto result = projector.build(std::move(polyline), configuration);
    testlib::check(result.category ==
        FailureCategory::ZeroLengthRejectedByConfiguration,
        "reject policy has a specific failure category");
}

TEST_CASE(empty_and_single_vertex_have_distinct_categories) {
    service::Configuration configuration;
    service::PolylineProjector projector;

    auto empty = projector.build(Polyline{}, configuration);
    testlib::check(empty.category == FailureCategory::EmptyPolyline,
        "empty polyline category is explicit");

    Polyline one_vertex{{{0, 0}}};
    auto single = projector.build(std::move(one_vertex), configuration);
    testlib::check(single.category == FailureCategory::InsufficientVertices,
        "single vertex category is explicit");
}
