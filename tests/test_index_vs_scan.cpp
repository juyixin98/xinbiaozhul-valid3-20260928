#include <random>
#include <vector>

#include "spatial/brute_force.hpp"
#include "spatial/bvh_index.hpp"
#include "test_util.hpp"

using namespace mileage;

namespace {

Polyline random_polyline(size_t n, uint64_t seed, cpp_int base, int64_t step_max) {
  std::mt19937_64 rng(seed);
  std::uniform_int_distribution<int64_t> step(-step_max, step_max);
  std::vector<Vec2> pts;
  pts.reserve(n);
  cpp_int x = base, y = base;
  pts.push_back({x, y});
  for (size_t i = 1; i < n; ++i) {
    x += step(rng);
    y += step(rng);
    pts.push_back({x, y});
  }
  return Polyline(std::move(pts));
}

void expect_same(const Polyline& line, const BvhIndex& idx, const Vec2& q) {
  const QueryResult ref = nearest_full_scan(line, q);
  IndexStats st;
  const QueryResult got = idx.nearest(q, &st);
  CHECK(got.status == ref.status);
  CHECK(same_candidate_set(got, ref));
  // The index may never test more segments than the scan.
  CHECK(st.segments_tested <= line.segment_count());
}

}  // namespace

// The accelerated index must be bit-identical to the full scan on every
// query, including ties, zero-length segments, and huge coordinates.
int main() {
  {
    // Moderate random polyline with zero-length segments injected.
    Polyline line = random_polyline(3001, 123, 0, 500);
    BvhIndex idx(line);
    std::mt19937_64 rng(99);
    std::uniform_int_distribution<int64_t> coord(-40000, 40000);
    for (int i = 0; i < 300; ++i) {
      expect_same(line, idx, Vec2{cpp_int(coord(rng)), cpp_int(coord(rng))});
    }
    // Endpoint projections: query exactly at vertices.
    for (size_t vtx = 0; vtx < line.vertex_count(); vtx += 97) {
      expect_same(line, idx, line.vertex(vtx));
    }
    // A far-away query must trigger real pruning.
    IndexStats st;
    const QueryResult far =
        idx.nearest(Vec2{cpp_int(100000000), cpp_int(100000000)}, &st);
    CHECK(far.accepted());
    CHECK(st.nodes_pruned > 0);
    CHECK(st.segments_tested < line.segment_count());
  }
  {
    // Polyline with duplicate consecutive vertices (zero-length segments).
    Polyline z({Vec2{0, 0}, Vec2{5, 0}, Vec2{5, 0}, Vec2{5, 0}, Vec2{5, 8},
                Vec2{12, 8}});
    BvhIndex idx(z);
    // The three-way tie at the duplicated vertex must survive pruning.
    expect_same(z, idx, Vec2{6, -1});
    expect_same(z, idx, Vec2{5, 0});
    expect_same(z, idx, Vec2{5, 3});
    const QueryResult r = idx.nearest(Vec2{6, -1});
    CHECK(r.candidates.size() == 3);
  }
  {
    // Huge coordinates near 10^18: exact arithmetic end to end.
    const cpp_int big("1000000000000000000");
    Polyline huge = random_polyline(501, 7, big, 1000000);
    BvhIndex idx(huge);
    std::mt19937_64 rng(8);
    std::uniform_int_distribution<int64_t> jitter(-2000000, 2000000);
    for (int i = 0; i < 60; ++i) {
      expect_same(huge, idx,
                  Vec2{big + jitter(rng), big + jitter(rng)});
    }
  }
  {
    // Hand-built equidistant configuration: symmetric V, query on the axis.
    Polyline v({Vec2{-10, 0}, Vec2{0, 10}, Vec2{10, 0}});
    BvhIndex idx(v);
    const QueryResult ref = nearest_full_scan(v, Vec2{0, 20});
    const QueryResult got = idx.nearest(Vec2{0, 20});
    CHECK(ref.candidates.size() == 2);  // equidistant to both legs
    CHECK(same_candidate_set(got, ref));
  }
  {
    // Empty polyline: both engines reject identically.
    Polyline empty({Vec2{3, 3}});
    BvhIndex idx(empty);
    const QueryResult r = idx.nearest(Vec2{0, 0});
    CHECK(r.status == QueryStatus::Rejected);
    CHECK(r.reason == "empty-polyline");
  }

  return testutil::summary("test_index_vs_scan");
}
