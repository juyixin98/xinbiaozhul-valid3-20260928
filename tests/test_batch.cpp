#include <sstream>

#include "batch/batch_runner.hpp"
#include "io/formats.hpp"
#include "test_util.hpp"

using namespace mileage;

namespace {

Polyline make(std::initializer_list<std::pair<long long, long long>> pts) {
  std::vector<Vec2> v;
  for (auto [x, y] : pts) v.push_back(Vec2{cpp_int(x), cpp_int(y)});
  return Polyline(std::move(v));
}

BatchQuery bq(std::string id, cpp_int x, cpp_int y, std::string label = "") {
  BatchQuery q;
  q.id = std::move(id);
  q.point = Vec2{std::move(x), std::move(y)};
  q.label = std::move(label);
  return q;
}

}  // namespace

int main() {
  Polyline L = make({{0, 0}, {10, 0}, {10, 10}});

  {
    // Acceptance, tie, and indeterminate decisions with diagnostics.
    BatchRunner runner(L, BatchOptions{});
    std::ostringstream diag;
    const auto outcomes = runner.run(
        {bq("q1", 5, 3, "user-secret-1"),
         bq("q2", 20, 0),
         bq("q3", cpp_int(1) << 130, 0)},
        &diag);
    CHECK(outcomes.size() == 3);

    CHECK(outcomes[0].result.status == QueryStatus::Accepted);
    CHECK(outcomes[0].result.candidates.size() == 1);
    CHECK(testutil::near(outcomes[0].result.candidates[0].mileage, 5.0L, 1e-9L));

    CHECK(outcomes[1].result.status == QueryStatus::Accepted);
    CHECK(outcomes[1].result.is_tie());
    CHECK(outcomes[1].result.reason == "ok-tie");

    CHECK(outcomes[2].result.status == QueryStatus::Indeterminate);
    CHECK(outcomes[2].result.reason == "coordinate-magnitude-beyond-limit");

    // Diagnostics carry the request id and the decision state...
    const std::string log = diag.str();
    CHECK(log.find("req=q1") != std::string::npos);
    CHECK(log.find("decision=ACCEPTED") != std::string::npos);
    CHECK(log.find("decision=INDETERMINATE") != std::string::npos);
    CHECK(log.find("tie=yes") != std::string::npos);
    // ...but sensitive labels are redacted to a fingerprint.
    CHECK(log.find("user-secret-1") == std::string::npos);
    CHECK(log.find("fnv8:") != std::string::npos);
  }
  {
    // Empty polyline: every request rejected with a stable reason.
    Polyline empty = make({{1, 1}});
    BatchRunner runner(empty, BatchOptions{});
    const BatchOutcome o = runner.run_one(bq("r1", 0, 0));
    CHECK(o.result.status == QueryStatus::Rejected);
    CHECK(o.result.reason == "empty-polyline");
    CHECK(o.diag_line.find("req=r1") != std::string::npos);
  }
  {
    // Index engine and scan engine agree through the batch entry point.
    BatchOptions scan_opts;
    scan_opts.use_index = false;
    BatchRunner with_index(L, BatchOptions{});
    BatchRunner with_scan(L, scan_opts);
    const BatchOutcome a = with_index.run_one(bq("c", 20, 0));
    const BatchOutcome b = with_scan.run_one(bq("c", 20, 0));
    CHECK(same_candidate_set(a.result, b.result));
  }
  {
    // Query file parsing: valid lines parsed, malformed lines reported.
    std::istringstream in(
        "# comment\n"
        "Q a 5 3 tag-one\n"
        "not-a-query\n"
        "Q b 20 0\n"
        "Q c xx 0\n");
    QueryFile qf = read_queries(in);
    CHECK(qf.queries.size() == 2);
    CHECK(qf.errors.size() == 2);
    CHECK(qf.queries[0].id == "a");
    CHECK(qf.queries[0].label == "tag-one");
    CHECK(qf.queries[1].label.empty());
  }
  {
    // Result records are round-trippable text with the request id.
    BatchRunner runner(L, BatchOptions{});
    const BatchOutcome o = runner.run_one(bq("w1", 12, 4));
    std::ostringstream out;
    write_result(out, o);
    const std::string line = out.str();
    CHECK(line.find("R w1 ACCEPTED") != std::string::npos);
    CHECK(line.find("seg=1") != std::string::npos);
    CHECK(line.find("t=2/5") != std::string::npos);
    CHECK(line.find("mileage=14.000000") != std::string::npos);
  }

  return testutil::summary("test_batch");
}
