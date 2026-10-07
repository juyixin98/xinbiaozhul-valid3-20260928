#pragma once

#include <cstddef>
#include <string>
#include <vector>

#include "geometry/types.hpp"

namespace mileage {

// One nearest-projection candidate. Ties produce multiple candidates; the
// candidate set is kept sorted by (seg_index) so results are stable across
// engines and runs.
struct Candidate {
  size_t seg_index = 0;
  Rat t;                 // exact clamped parameter in [0,1]
  Rat dist2;             // exact squared distance to the query point
  long double mileage = 0.0L;
};

bool same_candidate(const Candidate& a, const Candidate& b);

enum class QueryStatus { Accepted, Rejected, Indeterminate };

const char* to_string(QueryStatus s);

struct QueryResult {
  QueryStatus status = QueryStatus::Rejected;
  std::string reason;             // machine-readable reason token
  std::vector<Candidate> candidates;  // empty unless status == Accepted

  bool accepted() const { return status == QueryStatus::Accepted; }
  bool is_tie() const { return candidates.size() > 1; }
};

bool same_candidate_set(const QueryResult& a, const QueryResult& b);

}  // namespace mileage
