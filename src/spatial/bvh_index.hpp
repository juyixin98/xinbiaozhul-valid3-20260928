#pragma once

#include <cstdint>
#include <vector>

#include "geometry/polyline.hpp"
#include "geometry/projection.hpp"
#include "spatial/candidate_set.hpp"

namespace mileage {

struct IndexStats {
  uint64_t nodes_visited = 0;
  uint64_t leaves_visited = 0;
  uint64_t segments_tested = 0;
  uint64_t nodes_pruned = 0;   // queue entries discarded because lb > best
};

// Static BVH over per-segment AABBs with best-first exact lower-bound
// pruning. All lower bounds are exact (integer arithmetic on the lattice),
// and pruning only discards nodes whose lower bound is STRICTLY greater
// than the current best distance, so ties are never lost and the result is
// bit-identical to the full scan.
class BvhIndex {
 public:
  explicit BvhIndex(const Polyline& line);

  QueryResult nearest(const Vec2& q, IndexStats* stats = nullptr) const;

  // Segment indices whose AABBs intersect [lo,hi] (inclusive). Used by the
  // degenerate-case validator to find self-intersection candidate pairs.
  std::vector<size_t> query_aabb_overlap(const Vec2& lo, const Vec2& hi) const;

  size_t node_count() const { return nodes_.size(); }

 private:
  struct Node {
    Vec2 lo, hi;        // exact AABB
    int left = -1;      // child node indices, -1 for leaves
    int right = -1;
    size_t begin = 0;   // leaf: range into perm_
    size_t end = 0;
  };

  const Polyline* line_;
  std::vector<Node> nodes_;
  std::vector<size_t> perm_;  // segment indices in leaf order

  size_t build(size_t begin, size_t end);
};

}  // namespace mileage
