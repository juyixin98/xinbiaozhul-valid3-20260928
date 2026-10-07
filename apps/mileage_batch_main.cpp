#include <fstream>
#include <iostream>

#include "batch/batch_runner.hpp"
#include "io/formats.hpp"

// Batch entry point: mileage_batch <polyline-file> <query-file> <result-file> [--scan]
// Diagnostics (one redacted line per request) go to stderr.
int main(int argc, char** argv) {
  if (argc < 4) {
    std::cerr << "usage: mileage_batch <polyline-file> <query-file> <result-file> [--scan]\n";
    return 2;
  }
  std::ifstream pin(argv[1]), qin(argv[2]);
  if (!pin || !qin) {
    std::cerr << "cannot open input files\n";
    return 2;
  }
  mileage::PolylineFile pf = mileage::read_polyline(pin);
  mileage::QueryFile qf = mileage::read_queries(qin);
  for (const auto& e : pf.errors) {
    std::cerr << "polyline parse warning line " << e.line_no << ": " << e.message << "\n";
  }
  for (const auto& e : qf.errors) {
    std::cerr << "query parse warning line " << e.line_no << ": " << e.message << "\n";
  }

  mileage::BatchOptions opts;
  if (argc > 4 && std::string(argv[4]) == "--scan") opts.use_index = false;
  mileage::BatchRunner runner(pf.line, opts);

  std::ofstream rout(argv[3]);
  if (!rout) {
    std::cerr << "cannot open result file: " << argv[3] << "\n";
    return 2;
  }
  const auto outcomes = runner.run(qf.queries, &std::cerr);
  size_t accepted = 0, rejected = 0, indeterminate = 0;
  for (const auto& o : outcomes) {
    mileage::write_result(rout, o);
    switch (o.result.status) {
      case mileage::QueryStatus::Accepted: ++accepted; break;
      case mileage::QueryStatus::Rejected: ++rejected; break;
      case mileage::QueryStatus::Indeterminate: ++indeterminate; break;
    }
  }
  std::cerr << "batch done: accepted=" << accepted << " rejected=" << rejected
            << " indeterminate=" << indeterminate << "\n";
  return 0;
}
