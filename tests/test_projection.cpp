#include "geometry/polyline.hpp"
#include "spatial/brute_force.hpp"
#include "test_util.hpp"

using namespace mileage;

namespace {

Polyline make(std::initializer_list<std::pair<long long, long long>> pts) {
  std::vector<Vec2> v;
  for (auto [x, y] : pts) v.push_back(Vec2{cpp_int(x), cpp_int(y)});
  return Polyline(std::move(v));
}

Vec2 q(long long x, long long y) { return Vec2{cpp_int(x), cpp_int(y)}; }

bool has_candidate(const QueryResult& r, size_t seg, const cpp_int& tn,
                   const cpp_int& td) {
  for (const auto& c : r.candidates) {
    if (c.seg_index == seg && c.t.num == tn && c.t.den == td) return true;
  }
  return false;
}

}  // namespace

// All expected values below are hand-computed, not produced by the engine.
int main() {
  // L-shaped polyline: (0,0)-(10,0)-(10,10), chainage 0, 10, 20.
  Polyline L = make({{0, 0}, {10, 0}, {10, 10}});

  {
    // Interior projection onto the horizontal leg.
    QueryResult r = nearest_full_scan(L, q(5, 3));
    CHECK(r.accepted() && r.candidates.size() == 1);
    CHECK(has_candidate(r, 0, 1, 2));
    CHECK(r.candidates[0].dist2.num == 9 && r.candidates[0].dist2.den == 1);
    CHECK(testutil::near(r.candidates[0].mileage, 5.0L, 1e-9L));
  }
  {
    // Projection onto the vertical leg, t = 2/5.
    QueryResult r = nearest_full_scan(L, q(12, 4));
    CHECK(r.accepted() && r.candidates.size() == 1);
    CHECK(has_candidate(r, 1, 2, 5));
    CHECK(r.candidates[0].dist2.num == 4 && r.candidates[0].dist2.den == 1);
    CHECK(testutil::near(r.candidates[0].mileage, 14.0L, 1e-9L));
  }
  {
    // Endpoint projection: query at the shared vertex hits both segments.
    QueryResult r = nearest_full_scan(L, q(10, 0));
    CHECK(r.accepted() && r.candidates.size() == 2);
    CHECK(has_candidate(r, 0, 1, 1));
    CHECK(has_candidate(r, 1, 0, 1));
    CHECK(r.candidates[0].dist2.num == 0);
    CHECK(testutil::near(r.candidates[0].mileage, 10.0L, 1e-9L));
    CHECK(testutil::near(r.candidates[1].mileage, 10.0L, 1e-9L));
  }
  {
    // Equidistant tie: (20,0) is 10 units from both legs at the corner.
    QueryResult r = nearest_full_scan(L, q(20, 0));
    CHECK(r.accepted() && r.candidates.size() == 2);
    CHECK(r.is_tie());
    CHECK(has_candidate(r, 0, 1, 1));
    CHECK(has_candidate(r, 1, 0, 1));
    CHECK(r.candidates[0].dist2.num == 100);
    // Stable order: lower segment index first.
    CHECK(r.candidates[0].seg_index == 0 && r.candidates[1].seg_index == 1);
  }
  {
    // Polyline start/end vertices.
    QueryResult r0 = nearest_full_scan(L, q(0, 0));
    CHECK(r0.accepted() && r0.candidates.size() == 1);
    CHECK(has_candidate(r0, 0, 0, 1));
    CHECK(testutil::near(r0.candidates[0].mileage, 0.0L, 1e-9L));
    QueryResult r1 = nearest_full_scan(L, q(10, 10));
    CHECK(r1.accepted() && r1.candidates.size() == 1);
    CHECK(has_candidate(r1, 1, 1, 1));
    CHECK(testutil::near(r1.candidates[0].mileage, 20.0L, 1e-9L));
  }

  // Zero-length segment: (0,0)-(5,0)-(5,0)-(5,8). Chainage 0, 5, 5, 13.
  Polyline Z = make({{0, 0}, {5, 0}, {5, 0}, {5, 8}});
  {
    // (6,-1) is exactly sqrt(2) from the corner point via three segments:
    // end of s0, the zero-length s1, and the start of s2. All three must be
    // reported, all at chainage 5.
    QueryResult r = nearest_full_scan(Z, q(6, -1));
    CHECK(r.accepted() && r.candidates.size() == 3);
    CHECK(has_candidate(r, 0, 1, 1));
    CHECK(has_candidate(r, 1, 0, 1));
    CHECK(has_candidate(r, 2, 0, 1));
    for (const auto& c : r.candidates) {
      CHECK(c.dist2.num == 2 && c.dist2.den == 1);
      CHECK(testutil::near(c.mileage, 5.0L, 1e-9L));
    }
  }
  {
    // Interior of the vertical leg past the zero-length segment.
    QueryResult r = nearest_full_scan(Z, q(5, 3));
    CHECK(r.accepted() && r.candidates.size() == 1);
    CHECK(has_candidate(r, 2, 3, 8));
    CHECK(testutil::near(r.candidates[0].mileage, 8.0L, 1e-9L));
  }
  {
    // On the first leg.
    QueryResult r = nearest_full_scan(Z, q(3, 0));
    CHECK(r.accepted() && r.candidates.size() == 1);
    CHECK(has_candidate(r, 0, 3, 5));
    CHECK(testutil::near(r.candidates[0].mileage, 3.0L, 1e-9L));
  }

  // Empty polyline is rejected, not crashed on.
  Polyline empty = make({{1, 1}});
  QueryResult re = nearest_full_scan(empty, q(0, 0));
  CHECK(re.status == QueryStatus::Rejected);
  CHECK(re.reason == "empty-polyline");

  return testutil::summary("test_projection");
}
