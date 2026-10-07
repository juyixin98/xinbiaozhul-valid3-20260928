#pragma once

#include <cstddef>
#include <string>
#include <vector>

#include "geometry/polyline.hpp"

namespace mileage {

class BvhIndex;

enum class IssueKind {
  EmptyPolyline,       // fewer than 2 vertices: no segment, no mileage domain
  ZeroLengthSegment,   // consecutive identical vertices; mileage still defined
  DuplicateVertexRun,  // 3+ identical consecutive vertices (run of zero segs)
  SelfIntersection,    // non-adjacent segments crossing/touching/overlapping
  HugeCoordinate       // |coord| beyond 2^53: double-based mileage is lossy
};

const char* to_string(IssueKind k);

struct Issue {
  IssueKind kind;
  size_t seg_a = 0;   // segment (or vertex) index primarily involved
  size_t seg_b = 0;   // second segment for SelfIntersection, else == seg_a
  std::string detail; // human-readable, coordinate values redacted to magnitude
};

struct ValidationReport {
  std::vector<Issue> issues;

  bool ok() const { return issues.empty(); }
  size_t count(IssueKind k) const;
  std::string summary() const;
};

// Degenerate-case validation. Self-intersection uses the spatial index for
// pair candidate generation and exact predicates for confirmation, so it is
// O(n log n + k) instead of O(n^2). Adjacent segments sharing an endpoint are
// normal polyline connectivity and are not reported.
ValidationReport validate_polyline(const Polyline& line, const BvhIndex& accel);

}  // namespace mileage
