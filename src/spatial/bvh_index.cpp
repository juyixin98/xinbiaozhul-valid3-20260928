#include "spatial/bvh_index.hpp"

#include <algorithm>
#include <queue>

#include "geometry/predicates.hpp"

namespace mileage {

namespace {
constexpr size_t kLeafSize = 8;

Vec2 seg_lo(const Vec2& a, const Vec2& b) {
  return {a.x < b.x ? a.x : b.x, a.y < b.y ? a.y : b.y};
}
Vec2 seg_hi(const Vec2& a, const Vec2& b) {
  return {a.x > b.x ? a.x : b.x, a.y > b.y ? a.y : b.y};
}
}  // namespace

BvhIndex::BvhIndex(const Polyline& line) : line_(&line) {
  const size_t n = line.segment_count();
  perm_.resize(n);
  for (size_t i = 0; i < n; ++i) perm_[i] = i;
  if (n > 0) build(0, n);
}

size_t BvhIndex::build(size_t begin, size_t end) {
  Node node;
  // Union of segment AABBs.
  node.lo = seg_lo(line_->seg_a(perm_[begin]), line_->seg_b(perm_[begin]));
  node.hi = seg_hi(line_->seg_a(perm_[begin]), line_->seg_b(perm_[begin]));
  for (size_t i = begin + 1; i < end; ++i) {
    const Vec2 lo = seg_lo(line_->seg_a(perm_[i]), line_->seg_b(perm_[i]));
    const Vec2 hi = seg_hi(line_->seg_a(perm_[i]), line_->seg_b(perm_[i]));
    if (lo.x < node.lo.x) node.lo.x = lo.x;
    if (lo.y < node.lo.y) node.lo.y = lo.y;
    if (hi.x > node.hi.x) node.hi.x = hi.x;
    if (hi.y > node.hi.y) node.hi.y = hi.y;
  }

  const size_t my_index = nodes_.size();
  nodes_.push_back(std::move(node));

  if (end - begin <= kLeafSize) {
    nodes_[my_index].begin = begin;
    nodes_[my_index].end = end;
    return my_index;
  }

  // Split along the longest axis at the median of segment midpoints.
  const bool split_x = (nodes_[my_index].hi.x - nodes_[my_index].lo.x) >=
                       (nodes_[my_index].hi.y - nodes_[my_index].lo.y);
  const size_t mid = begin + (end - begin) / 2;
  std::nth_element(perm_.begin() + begin, perm_.begin() + mid,
                   perm_.begin() + end, [&](size_t s1, size_t s2) {
                     const Vec2 a1 = line_->seg_a(s1), b1 = line_->seg_b(s1);
                     const Vec2 a2 = line_->seg_a(s2), b2 = line_->seg_b(s2);
                     const cpp_int c1 = split_x ? a1.x + b1.x : a1.y + b1.y;
                     const cpp_int c2 = split_x ? a2.x + b2.x : a2.y + b2.y;
                     if (c1 != c2) return c1 < c2;
                     return s1 < s2;  // deterministic order
                   });
  nodes_[my_index].left = static_cast<int>(build(begin, mid));
  nodes_[my_index].right = static_cast<int>(build(mid, end));
  return my_index;
}

QueryResult BvhIndex::nearest(const Vec2& q, IndexStats* stats) const {
  if (nodes_.empty()) {
    QueryResult r;
    r.status = QueryStatus::Rejected;
    r.reason = "empty-polyline";
    return r;
  }

  struct Entry {
    Rat lb;  // exact lower bound on squared distance to this node's AABB
    size_t node;
  };
  struct EntryLater {
    bool operator()(const Entry& a, const Entry& b) const {
      return Rat::cmp(a.lb, b.lb) > 0;  // min-heap on lb
    }
  };
  std::priority_queue<Entry, std::vector<Entry>, EntryLater> pq;
  pq.push(Entry{Rat{dist2_point_aabb(q, nodes_[0].lo, nodes_[0].hi), cpp_int(1)}, 0});

  std::vector<Candidate> best;
  while (!pq.empty()) {
    const Entry e = pq.top();
    // Best-first order: once the smallest queued lower bound exceeds the
    // current best, every remaining entry is also worse. Equality is kept,
    // so tied candidates can never be pruned away.
    if (!best.empty() && Rat::cmp(e.lb, best.front().dist2) > 0) {
      if (stats) stats->nodes_pruned += pq.size();
      break;
    }
    pq.pop();
    if (stats) ++stats->nodes_visited;
    const Node& n = nodes_[e.node];
    if (n.left < 0) {
      if (stats) ++stats->leaves_visited;
      for (size_t i = n.begin; i < n.end; ++i) {
        const size_t seg = perm_[i];
        if (stats) ++stats->segments_tested;
        Candidate c;
        c.seg_index = seg;
        c.dist2 = dist2_point_segment(q, line_->seg_a(seg), line_->seg_b(seg), c.t);
        if (best.empty() || Rat::cmp(c.dist2, best.front().dist2) < 0) {
          best.clear();
          best.push_back(c);
        } else if (c.dist2 == best.front().dist2) {
          best.push_back(c);
        }
      }
    } else {
      for (int child : {n.left, n.right}) {
        const Node& cn = nodes_[child];
        Entry ce{Rat{dist2_point_aabb(q, cn.lo, cn.hi), cpp_int(1)},
                 static_cast<size_t>(child)};
        // Prune eagerly only when strictly worse than the current best.
        if (!best.empty() && Rat::cmp(ce.lb, best.front().dist2) > 0) {
          if (stats) ++stats->nodes_pruned;
          continue;
        }
        pq.push(std::move(ce));
      }
    }
  }
  return finalize_candidates(*line_, std::move(best));
}

std::vector<size_t> BvhIndex::query_aabb_overlap(const Vec2& lo,
                                                 const Vec2& hi) const {
  std::vector<size_t> out;
  if (nodes_.empty()) return out;
  std::vector<size_t> stack{0};
  while (!stack.empty()) {
    const Node& n = nodes_[stack.back()];
    stack.pop_back();
    if (n.hi.x < lo.x || n.lo.x > hi.x || n.hi.y < lo.y || n.lo.y > hi.y) {
      continue;
    }
    if (n.left < 0) {
      for (size_t i = n.begin; i < n.end; ++i) {
        const size_t seg = perm_[i];
        const Vec2 slo = seg_lo(line_->seg_a(seg), line_->seg_b(seg));
        const Vec2 shi = seg_hi(line_->seg_a(seg), line_->seg_b(seg));
        if (shi.x >= lo.x && slo.x <= hi.x && shi.y >= lo.y && slo.y <= hi.y) {
          out.push_back(seg);
        }
      }
    } else {
      stack.push_back(static_cast<size_t>(n.left));
      stack.push_back(static_cast<size_t>(n.right));
    }
  }
  return out;
}

}  // namespace mileage
