#include "geometry/predicates.hpp"
#include "test_util.hpp"

using namespace mileage;

namespace {

Vec2 v(long long x, long long y) { return Vec2{cpp_int(x), cpp_int(y)}; }

}  // namespace

int main() {
  // Orientation with coordinates where double rounding flips the sign:
  // cross = N*(N+1) - N*N = N, but in double both products round to N^2.
  const cpp_int N = cpp_int(1) << 62;
  const Vec2 o{0, 0};
  const Vec2 a{N, N};
  const Vec2 b{N, N + 1};
  CHECK(orient2d(o, a, b) == 1);
  CHECK(orient2d(o, b, a) == -1);
  CHECK(orient2d(o, a, Vec2{2 * N, 2 * N}) == 0);

  // Segment relations, all hand-classified.
  CHECK(segment_relation(v(0, 0), v(10, 0), v(5, -5), v(5, 5)) ==
        SegRelation::ProperCross);
  CHECK(segment_relation(v(0, 0), v(10, 0), v(10, 0), v(10, 10)) ==
        SegRelation::EndpointTouch);
  CHECK(segment_relation(v(0, 0), v(10, 0), v(5, 0), v(15, 0)) ==
        SegRelation::CollinearOverlap);
  CHECK(segment_relation(v(0, 0), v(10, 0), v(10, 0), v(20, 0)) ==
        SegRelation::CollinearTouch);
  CHECK(segment_relation(v(0, 0), v(10, 0), v(10, 0), v(0, 0)) ==
        SegRelation::Identical);
  CHECK(segment_relation(v(0, 0), v(10, 0), v(0, 1), v(10, 1)) ==
        SegRelation::Disjoint);

  // Zero-length segment behaves as a point.
  CHECK(segment_relation(v(5, 5), v(5, 5), v(0, 0), v(10, 10)) ==
        SegRelation::EndpointTouch);
  CHECK(segment_relation(v(5, 5), v(5, 5), v(0, 0), v(1, 1)) ==
        SegRelation::Disjoint);

  // Exact point-segment squared distances, hand-computed.
  Rat t;
  Rat d = dist2_point_segment(v(3, 4), v(0, 0), v(0, 0), t);
  CHECK(d.num == 25 && d.den == 1);  // zero-length segment: point distance
  CHECK(t.num == 0);

  d = dist2_point_segment(v(5, 3), v(0, 0), v(10, 0), t);
  CHECK(d.num == 9 && d.den == 1);  // interior projection
  CHECK(t.num == 1 && t.den == 2);

  d = dist2_point_segment(v(-4, 3), v(0, 0), v(10, 0), t);
  CHECK(d.num == 25 && d.den == 1);  // clamped to start endpoint
  CHECK(t.num == 0);

  d = dist2_point_segment(v(14, 3), v(0, 0), v(10, 0), t);
  CHECK(d.num == 25 && d.den == 1);  // clamped to end endpoint
  CHECK(t.num == 1 && t.den == 1);

  // Slanted interior projection: p=(0,1) onto (0,0)-(2,2): t=1/2, dist2=1/2.
  d = dist2_point_segment(v(0, 1), v(0, 0), v(2, 2), t);
  CHECK(d.num == 1 && d.den == 2);
  CHECK(t.num == 1 && t.den == 2);

  // AABB lower bounds.
  CHECK(dist2_point_aabb(v(5, 5), v(0, 0), v(2, 3)) == 13);
  CHECK(dist2_point_aabb(v(1, 1), v(0, 0), v(2, 3)) == 0);

  return testutil::summary("test_predicates");
}
