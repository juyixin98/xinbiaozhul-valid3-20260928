#include "test_framework.hpp"

#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/reference_scan.hpp"
#include "nearest/spatial/bvh_index.hpp"

#include <set>

using namespace nearest;

TEST_CASE(bvh_matches_independent_scan_on_fixed_self_intersecting_fixture) {
    Polyline polyline{{
        {0, 0}, {0, 0}, {4, 0}, {4, 8}, {-10, 20},
        {20, -10}, {0, 0}, {20, 20}, {-5, 30},
        {30, -5}, {40, 0}, {40, 40}, {0, 40}
    }};

    spatial::BvhIndex bvh(polyline);
    testlib::check(bvh.nodeCount() >= polyline.segmentCount(),
        "index has a real tree rather than an empty shell");

    for (long long x = -12; x <= 42; x += 3) {
        for (long long y = -12; y <= 42; y += 3) {
            RationalPoint query{Rational(x), Rational(y)};
            QueryOutcome fast = bvh.nearest(query);
            QueryOutcome scan = reference::exhaustiveNearestProjection(polyline, query);

            std::set<std::size_t> fast_indices;
            std::set<std::size_t> scan_indices;
            for (const auto& c : fast.candidates) {
                fast_indices.insert(c.segment_index);
            }
            for (const auto& c : scan.candidates) {
                scan_indices.insert(c.segment_index);
            }

            testlib::check(fast_indices == scan_indices,
                "BVH and scan tie sets agree at grid point " +
                std::to_string(x) + "," + std::to_string(y));
            testlib::check(!fast.candidates.empty(), "grid query has optimum");
            testlib::check(fast.candidates.front().distance_squared ==
                scan.candidates.front().distance_squared,
                "BVH optimum squared distance matches scan");
        }
    }
}

TEST_CASE(pruning_actually_occurs_without_losing_exact_optimum) {
    Polyline polyline;
    polyline.vertices.push_back({0, 0});
    for (long long i = 1; i <= 512; ++i) {
        long long y = (i % 2 == 0) ? 20 : 0;
        polyline.vertices.push_back({i * 2, y});
    }

    spatial::BvhIndex bvh(polyline);
    QueryOutcome outcome = bvh.nearest({1020, 10});
    QueryOutcome scan = reference::exhaustiveNearestProjection(
        polyline, {1020, 10});

    testlib::check(outcome.nodes_pruned > 0, "distant boxes were pruned");
    testlib::check(outcome.leaves_visited < polyline.segmentCount(),
        "fewer exact segment evaluations than full scan");
    testlib::check(outcome.candidates.size() == scan.candidates.size(),
        "pruning preserved tie count");
    testlib::check(outcome.candidates.front().distance_squared ==
        scan.candidates.front().distance_squared,
        "pruning preserved exact optimum");
}
