#include "geometry/projection.hpp"

namespace mileage {

bool same_candidate(const Candidate& a, const Candidate& b) {
  return a.seg_index == b.seg_index && a.t == b.t && a.dist2 == b.dist2;
}

const char* to_string(QueryStatus s) {
  switch (s) {
    case QueryStatus::Accepted: return "ACCEPTED";
    case QueryStatus::Rejected: return "REJECTED";
    case QueryStatus::Indeterminate: return "INDETERMINATE";
  }
  return "?";
}

bool same_candidate_set(const QueryResult& a, const QueryResult& b) {
  if (a.status != b.status) return false;
  if (a.candidates.size() != b.candidates.size()) return false;
  for (size_t i = 0; i < a.candidates.size(); ++i) {
    if (!same_candidate(a.candidates[i], b.candidates[i])) return false;
  }
  return true;
}

}  // namespace mileage
