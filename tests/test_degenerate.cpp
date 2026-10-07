#include "spatial/bvh_index.hpp"
#include "test_util.hpp"
#include "validate/degenerate.hpp"

using namespace mileage;

namespace {

Polyline make(std::initializer_list<std::pair<long long, long long>> pts) {
  std::vector<Vec2> v;
  for (auto [x, y] : pts) v.push_back(Vec2{cpp_int(x), cpp_int(y)});
  return Polyline(std::move(v));
}

bool has_self_intersection(const ValidationReport& r, size_t a, size_t b) {
  for (const auto& i : r.issues) {
    if (i.kind == IssueKind::SelfIntersection && i.seg_a == a && i.seg_b == b) {
      return true;
    }
  }
  return false;
}

}  // namespace

int main() {
  {
    // Empty polyline is its own failure category.
    Polyline empty = make({{7, 7}});
    BvhIndex idx(empty);
    const ValidationReport rep = validate_polyline(empty, idx);
    CHECK(rep.count(IssueKind::EmptyPolyline) == 1);
    CHECK(!rep.ok());
  }
  {
    // Zero-length segment: chainage semantics preserved, and the honest
    // side effect is reported: the segments before and after the zero-length
    // segment touch at the duplicated vertex (a real self-contact).
    Polyline z = make({{0, 0}, {5, 0}, {5, 0}, {5, 8}});
    BvhIndex idx(z);
    const ValidationReport rep = validate_polyline(z, idx);
    CHECK(rep.count(IssueKind::ZeroLengthSegment) == 1);
    CHECK(rep.count(IssueKind::DuplicateVertexRun) == 0);
    CHECK(has_self_intersection(rep, 0, 2));
  }
  {
    // Run of two consecutive zero-length segments.
    Polyline run = make({{0, 0}, {5, 0}, {5, 0}, {5, 0}, {5, 8}});
    BvhIndex idx(run);
    const ValidationReport rep = validate_polyline(run, idx);
    CHECK(rep.count(IssueKind::ZeroLengthSegment) == 2);
    CHECK(rep.count(IssueKind::DuplicateVertexRun) == 1);
  }
  {
    // Figure with one proper crossing: seg 0 (0,0)-(10,0) crosses
    // seg 3 (0,10)-(5,-1) at (50/11, 0). Hand-verified orientation signs.
    Polyline fig = make({{0, 0}, {10, 0}, {10, 10}, {0, 10}, {5, -1}});
    BvhIndex idx(fig);
    const ValidationReport rep = validate_polyline(fig, idx);
    CHECK(rep.count(IssueKind::SelfIntersection) == 1);
    CHECK(has_self_intersection(rep, 0, 3));
    CHECK(rep.count(IssueKind::ZeroLengthSegment) == 0);
  }
  {
    // Clean polyline: adjacent shared endpoints are NOT self-intersections.
    Polyline ok = make({{0, 0}, {10, 0}, {10, 10}, {20, 10}});
    BvhIndex idx(ok);
    const ValidationReport rep = validate_polyline(ok, idx);
    CHECK(rep.ok());
  }
  {
    // Huge coordinate beyond 2^53 is flagged (double chainage would be lossy).
    Polyline huge({Vec2{0, 0}, Vec2{cpp_int(1) << 60, cpp_int(0)}});
    BvhIndex idx(huge);
    const ValidationReport rep = validate_polyline(huge, idx);
    CHECK(rep.count(IssueKind::HugeCoordinate) == 1);
  }
  {
    // Report details never echo raw coordinate values, only magnitudes.
    Polyline huge({Vec2{0, 0}, Vec2{cpp_int("98765432109876543210"), cpp_int(0)}});
    BvhIndex idx(huge);
    const ValidationReport rep = validate_polyline(huge, idx);
    CHECK(rep.count(IssueKind::HugeCoordinate) == 1);
    for (const auto& i : rep.issues) {
      CHECK(i.detail.find("98765432109876543210") == std::string::npos);
    }
  }

  return testutil::summary("test_degenerate");
}
