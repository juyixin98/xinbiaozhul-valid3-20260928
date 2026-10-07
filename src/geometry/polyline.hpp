#pragma once

#include <cstddef>
#include <vector>

#include "geometry/types.hpp"

namespace mileage {

// An open polyline on the exact integer lattice. Mileage (chainage) is the
// accumulated Euclidean length along the vertices, kept in long double for
// output only; all ordering decisions elsewhere use exact arithmetic.
//
// Zero-length segments (consecutive duplicate vertices) are preserved as
// real segments: they contribute zero length but carry a well-defined
// mileage equal to the accumulated length at that vertex.
class Polyline {
 public:
  Polyline() = default;
  explicit Polyline(std::vector<Vec2> pts);

  size_t vertex_count() const { return pts_.size(); }
  size_t segment_count() const { return pts_.empty() ? 0 : pts_.size() - 1; }

  const Vec2& vertex(size_t i) const { return pts_[i]; }
  Vec2 seg_a(size_t i) const { return pts_[i]; }
  Vec2 seg_b(size_t i) const { return pts_[i + 1]; }

  bool zero_length(size_t seg) const { return pts_[seg] == pts_[seg + 1]; }

  // Accumulated length at vertex i (prefix_len_[0] == 0).
  long double prefix_length(size_t i) const { return prefix_len_[i]; }
  long double segment_length(size_t seg) const { return seg_len_[seg]; }
  long double total_length() const { return prefix_len_.back(); }

  // Mileage of the point at exact parameter t on segment `seg`.
  long double mileage_at(size_t seg, const Rat& t) const;

 private:
  std::vector<Vec2> pts_;
  std::vector<long double> prefix_len_;  // size == pts_.size()
  std::vector<long double> seg_len_;     // size == segment_count()
};

}  // namespace mileage
