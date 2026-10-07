#include "spatial/brute_force.hpp"

#include "geometry/predicates.hpp"

namespace mileage {

QueryResult nearest_full_scan(const Polyline& line, const Vec2& q,
                              ScanStats* stats) {
  if (line.segment_count() == 0) {
    QueryResult r;
    r.status = QueryStatus::Rejected;
    r.reason = "empty-polyline";
    return r;
  }
  std::vector<Candidate> best;
  for (size_t i = 0; i < line.segment_count(); ++i) {
    if (stats) ++stats->segments_examined;
    Candidate c;
    c.seg_index = i;
    c.dist2 = dist2_point_segment(q, line.seg_a(i), line.seg_b(i), c.t);
    if (best.empty() || Rat::cmp(c.dist2, best.front().dist2) < 0) {
      best.clear();
      best.push_back(c);
    } else if (c.dist2 == best.front().dist2) {
      best.push_back(c);
    }
  }
  return finalize_candidates(line, std::move(best));
}

}  // namespace mileage
