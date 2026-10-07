#include "validate/degenerate.hpp"

#include <algorithm>
#include <sstream>

#include "geometry/predicates.hpp"
#include "spatial/bvh_index.hpp"

namespace mileage {

const char* to_string(IssueKind k) {
  switch (k) {
    case IssueKind::EmptyPolyline: return "EmptyPolyline";
    case IssueKind::ZeroLengthSegment: return "ZeroLengthSegment";
    case IssueKind::DuplicateVertexRun: return "DuplicateVertexRun";
    case IssueKind::SelfIntersection: return "SelfIntersection";
    case IssueKind::HugeCoordinate: return "HugeCoordinate";
  }
  return "?";
}

size_t ValidationReport::count(IssueKind k) const {
  return static_cast<size_t>(
      std::count_if(issues.begin(), issues.end(),
                    [&](const Issue& i) { return i.kind == k; }));
}

std::string ValidationReport::summary() const {
  std::ostringstream os;
  os << "issues=" << issues.size()
     << " zero_length=" << count(IssueKind::ZeroLengthSegment)
     << " dup_runs=" << count(IssueKind::DuplicateVertexRun)
     << " self_intersections=" << count(IssueKind::SelfIntersection)
     << " huge_coords=" << count(IssueKind::HugeCoordinate)
     << " empty=" << count(IssueKind::EmptyPolyline);
  return os.str();
}

namespace {

// Coordinate magnitudes only; absolute values are never echoed into reports.
std::string magnitude_note(const cpp_int& v) {
  cpp_int a = v < 0 ? -v : v;
  size_t digits = 0;
  while (a > 0) {
    a /= 10;
    ++digits;
  }
  std::ostringstream os;
  os << "magnitude~10^" << digits;
  return os.str();
}

}  // namespace

ValidationReport validate_polyline(const Polyline& line, const BvhIndex& accel) {
  ValidationReport rep;
  const size_t n = line.vertex_count();
  if (n < 2) {
    rep.issues.push_back(
        {IssueKind::EmptyPolyline, 0, 0, "polyline has fewer than 2 vertices"});
    return rep;
  }

  // Zero-length segments and duplicate runs.
  size_t run_start = 0;
  for (size_t i = 0; i < line.segment_count(); ++i) {
    if (line.zero_length(i)) {
      rep.issues.push_back({IssueKind::ZeroLengthSegment, i, i,
                            "segment contributes zero length; mileage stays at "
                            "the accumulated chainage of its vertex"});
    } else {
      if (i > run_start + 1) {
        rep.issues.push_back({IssueKind::DuplicateVertexRun, run_start, i - 1,
                              "run of consecutive zero-length segments"});
      }
      run_start = i + 1;
    }
  }
  if (line.segment_count() > run_start + 1) {
    rep.issues.push_back({IssueKind::DuplicateVertexRun, run_start,
                          line.segment_count() - 1,
                          "run of consecutive zero-length segments"});
  }

  // Huge coordinates: beyond 2^53 a double chainage cannot represent every
  // lattice point, so downstream float consumers must be warned.
  const cpp_int kHuge = cpp_int(1) << 53;
  for (size_t i = 0; i < n; ++i) {
    const Vec2& p = line.vertex(i);
    const cpp_int ax = p.x < 0 ? -p.x : p.x;
    const cpp_int ay = p.y < 0 ? -p.y : p.y;
    if (ax > kHuge || ay > kHuge) {
      rep.issues.push_back({IssueKind::HugeCoordinate, i, i,
                            "coordinate " + magnitude_note(ax > ay ? ax : ay) +
                                " exceeds 2^53; double chainage is lossy"});
    }
  }

  // Self-intersections: index-accelerated pair generation, exact predicates.
  for (size_t i = 0; i < line.segment_count(); ++i) {
    const Vec2 a = line.seg_a(i), b = line.seg_b(i);
    const Vec2 lo{a.x < b.x ? a.x : b.x, a.y < b.y ? a.y : b.y};
    const Vec2 hi{a.x > b.x ? a.x : b.x, a.y > b.y ? a.y : b.y};
    for (size_t j : accel.query_aabb_overlap(lo, hi)) {
      if (j <= i) continue;
      // Adjacent segments legitimately share an endpoint.
      if (j == i + 1) continue;
      const SegRelation rel =
          segment_relation(a, b, line.seg_a(j), line.seg_b(j));
      if (rel == SegRelation::Disjoint) continue;
      std::ostringstream os;
      os << "segments relate as " << to_string(rel);
      rep.issues.push_back({IssueKind::SelfIntersection, i, j, os.str()});
    }
  }
  return rep;
}

}  // namespace mileage
