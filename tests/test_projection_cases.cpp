#include "test_framework.hpp"

#include "nearest/geometry/decimal.hpp"
#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/predicates.hpp"
#include "nearest/geometry/reference_scan.hpp"
#include "nearest/spatial/bvh_index.hpp"

#include <vector>

using namespace nearest;

namespace {

RationalPoint p(long long x, long long y) {
    return {Rational(x), Rational(y)};
}

std::vector<std::size_t> indices(const QueryOutcome& outcome) {
    std::vector<std::size_t> result;
    for (const auto& candidate : outcome.candidates) {
        result.push_back(candidate.segment_index);
    }
    return result;
}

void expectIndices(const QueryOutcome& outcome,
    const std::vector<std::size_t>& expected,
    const std::string& label) {
    testlib::check(indices(outcome) == expected, label);
}

}  // namespace

TEST_CASE(retained_zero_segment_ties_with_normal_adjacent_segment) {
    Polyline polyline{{p(0, 0), p(0, 0), p(2, 0), p(2, 2)}};
    spatial::BvhIndex bvh(polyline);
    auto fast = bvh.nearest(p(0, 1));
    auto reference = reference::exhaustiveNearestProjection(polyline, p(0, 1));

    expectIndices(fast, {0, 1}, "zero and next segment both at distance 1");
    expectIndices(reference, {0, 1}, "reference independently gives {0,1}");
    testlib::check(fast.candidates.front().distance_squared ==
        reference.candidates.front().distance_squared,
        "distance is exact");
}

TEST_CASE(equidistant_corner_returns_stable_full_candidate_set) {
    Polyline polyline{{p(0, 0), p(0, 0), p(2, 0), p(2, 2)}};
    spatial::BvhIndex bvh(polyline);
    auto outcome = bvh.nearest(p(1, 1));
    expectIndices(outcome, {1, 2}, "two equidistant normal projections sorted");
}

TEST_CASE(interior_endpoint_ties_both_adjacent_segments) {
    Polyline polyline{{p(0, 0), p(1, 0), p(2, 0)}};
    spatial::BvhIndex bvh(polyline);
    auto outcome = bvh.nearest(p(1, 0));
    expectIndices(outcome, {0, 1}, "endpoint projection is reported on both adjacent segments");
}

TEST_CASE(self_intersecting_polyline_returns_crossing_segments) {
    Polyline polyline{{
        p(100, 100),
        p(0, 1000),
        p(1000, 0),
        p(0, 0),
        p(1000, 1000)
    }};
    spatial::BvhIndex bvh(polyline);
    auto fast = bvh.nearest(p(500, 500));
    auto scan = reference::exhaustiveNearestProjection(polyline, p(500, 500));

    expectIndices(fast, {1, 3}, "both crossing diagonals contain the query");
    expectIndices(scan, {1, 3}, "reference scan agrees at crossing");
    testlib::check(fast.candidates.front().distance_squared == 0,
        "intersection distance is zero");
}

TEST_CASE(giant_coordinates_keep_exact_midpoint_and_endpoint) {
    auto x0 = parseDecimalRational("100000000000000000000000000000");
    auto x1 = parseDecimalRational("300000000000000000000000000000");
    testlib::check(x0.has_value() && x1.has_value(), "giant decimals parsed exactly");

    Polyline polyline{{{*x0, *x0}, {*x1, *x1}}};
    spatial::BvhIndex bvh(polyline);
    auto midpoint_x = parseDecimalRational("200000000000000000000000000000");
    RationalPoint query{*midpoint_x + 1, *midpoint_x - 1};
    auto outcome = bvh.nearest(query);

    expectIndices(outcome, {0}, "one giant segment candidate");
    testlib::check(outcome.candidates.front().t == Rational(1, 2),
        "t is exact one half despite huge coordinates");
    testlib::check(outcome.candidates.front().distance_squared == 2,
        "normal offset of (1,-1) has squared distance 2");

    auto endpoint = bvh.nearest({*x1, *x1});
    testlib::check(endpoint.candidates.size() == 1, "endpoint one candidate");
    testlib::check(endpoint.candidates.front().t == 1, "endpoint t is one");
}
