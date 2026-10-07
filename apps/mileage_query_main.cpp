#include <fstream>
#include <iostream>
#include <string>

#include "batch/batch_runner.hpp"
#include "io/formats.hpp"

// Single-query CLI: mileage_query <polyline-file> <x> <y> [--scan]
int main(int argc, char** argv) {
  if (argc < 4) {
    std::cerr << "usage: mileage_query <polyline-file> <x> <y> [--scan]\n";
    return 2;
  }
  std::ifstream in(argv[1]);
  if (!in) {
    std::cerr << "cannot open polyline file: " << argv[1] << "\n";
    return 2;
  }
  mileage::PolylineFile pf = mileage::read_polyline(in);
  for (const auto& e : pf.errors) {
    std::cerr << "parse warning line " << e.line_no << ": " << e.message << "\n";
  }

  mileage::BatchQuery q;
  q.id = "cli";
  try {
    q.point.x = mileage::cpp_int(argv[2]);
    q.point.y = mileage::cpp_int(argv[3]);
  } catch (...) {
    std::cerr << "coordinates must be decimal integers\n";
    return 2;
  }

  mileage::BatchOptions opts;
  if (argc > 4 && std::string(argv[4]) == "--scan") opts.use_index = false;
  mileage::BatchRunner runner(pf.line, opts);
  mileage::BatchOutcome o = runner.run_one(q);
  mileage::write_result(std::cout, o);
  std::cerr << o.diag_line << "\n";
  return o.result.accepted() ? 0 : 1;
}
