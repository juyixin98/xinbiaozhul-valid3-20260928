#include "batch/batch_runner.hpp"

#include <ostream>
#include <sstream>

#include "spatial/brute_force.hpp"

namespace mileage {

BatchRunner::BatchRunner(const Polyline& line, BatchOptions opts)
    : line_(line), opts_(std::move(opts)), index_(line) {}

BatchOutcome BatchRunner::run_one(const BatchQuery& q) const {
  BatchOutcome out;
  out.query = q;

  std::ostringstream diag;
  diag << "[req=" << (q.id.empty() ? "-" : q.id) << "]";
  // The label is sensitive: only a short fingerprint is ever logged.
  diag << " label=" << redact_label(q.label);

  if (line_.segment_count() == 0) {
    out.result.status = QueryStatus::Rejected;
    out.result.reason = "empty-polyline";
    diag << " decision=REJECTED reason=empty-polyline";
    out.diag_line = diag.str();
    return out;
  }

  const cpp_int ax = q.point.x < 0 ? -q.point.x : q.point.x;
  const cpp_int ay = q.point.y < 0 ? -q.point.y : q.point.y;
  if (ax > opts_.coord_limit || ay > opts_.coord_limit) {
    out.result.status = QueryStatus::Indeterminate;
    out.result.reason = "coordinate-magnitude-beyond-limit";
    diag << " decision=INDETERMINATE reason=coordinate-magnitude-beyond-limit";
    out.diag_line = diag.str();
    return out;
  }

  if (opts_.use_index) {
    IndexStats st;
    out.result = index_.nearest(q.point, &st);
    diag << " engine=index nodes_visited=" << st.nodes_visited
         << " leaves_visited=" << st.leaves_visited
         << " segments_tested=" << st.segments_tested
         << " nodes_pruned=" << st.nodes_pruned;
  } else {
    ScanStats st;
    out.result = nearest_full_scan(line_, q.point, &st);
    diag << " engine=scan segments_examined=" << st.segments_examined;
  }

  diag << " decision=" << to_string(out.result.status)
       << " reason=" << out.result.reason
       << " candidates=" << out.result.candidates.size();
  if (out.result.accepted()) {
    const Candidate& c = out.result.candidates.front();
    diag << " best_seg=" << c.seg_index << " best_dist2=" << to_string(c.dist2);
    if (out.result.is_tie()) diag << " tie=yes";
  }
  out.diag_line = diag.str();
  return out;
}

std::vector<BatchOutcome> BatchRunner::run(const std::vector<BatchQuery>& queries,
                                           std::ostream* diag) const {
  std::vector<BatchOutcome> out;
  out.reserve(queries.size());
  for (const auto& q : queries) {
    out.push_back(run_one(q));
    if (diag) *diag << out.back().diag_line << '\n';
  }
  return out;
}

}  // namespace mileage
