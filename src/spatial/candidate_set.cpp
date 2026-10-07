#include "spatial/candidate_set.hpp"

#include <algorithm>

namespace mileage {

QueryResult finalize_candidates(const Polyline& line,
                                std::vector<Candidate> best) {
  QueryResult out;
  if (best.empty()) {
    out.status = QueryStatus::Rejected;
    out.reason = "no-candidate";
    return out;
  }
  // Stable candidate set: sort by segment index, then exact t. Segment index
  // order is the polyline's own order, so ties are reproducible run to run.
  std::sort(best.begin(), best.end(), [](const Candidate& a, const Candidate& b) {
    if (a.seg_index != b.seg_index) return a.seg_index < b.seg_index;
    return Rat::cmp(a.t, b.t) < 0;
  });
  for (auto& c : best) {
    c.mileage = line.mileage_at(c.seg_index, c.t);
  }
  out.status = QueryStatus::Accepted;
  out.reason = best.size() > 1 ? "ok-tie" : "ok";
  out.candidates = std::move(best);
  return out;
}

}  // namespace mileage
