#pragma once

#include <iosfwd>
#include <string>
#include <vector>

#include "geometry/polyline.hpp"
#include "geometry/projection.hpp"
#include "spatial/bvh_index.hpp"

namespace mileage {

struct BatchQuery {
  std::string id;     // request identifier, echoed into results and diagnostics
  Vec2 point;
  std::string label;  // optional free-form tag; treated as sensitive, never logged raw
};

struct BatchOptions {
  bool use_index = true;
  // Queries whose |coordinate| exceeds this limit cannot be safely assigned a
  // floating chainage; they are answered INDETERMINATE instead of guessed.
  cpp_int coord_limit = cpp_int(1) << 126;
};

struct BatchOutcome {
  BatchQuery query;
  QueryResult result;
  std::string diag_line;  // single-line diagnostic, sensitive fields redacted
};

// Batch entry point: applies per-request validation, dispatches to the index
// or the full scan, and emits one redacted diagnostic line per request
// explaining why the request was accepted, rejected, or left indeterminate.
class BatchRunner {
 public:
  BatchRunner(const Polyline& line, BatchOptions opts);

  BatchOutcome run_one(const BatchQuery& q) const;
  std::vector<BatchOutcome> run(const std::vector<BatchQuery>& queries,
                                std::ostream* diag = nullptr) const;

 private:
  const Polyline& line_;
  BatchOptions opts_;
  BvhIndex index_;
};

}  // namespace mileage
