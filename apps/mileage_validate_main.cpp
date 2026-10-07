#include <fstream>
#include <iostream>

#include "io/formats.hpp"
#include "spatial/bvh_index.hpp"
#include "validate/degenerate.hpp"

// Degenerate-case validation CLI: mileage_validate <polyline-file>
int main(int argc, char** argv) {
  if (argc < 2) {
    std::cerr << "usage: mileage_validate <polyline-file>\n";
    return 2;
  }
  std::ifstream in(argv[1]);
  if (!in) {
    std::cerr << "cannot open polyline file: " << argv[1] << "\n";
    return 2;
  }
  mileage::PolylineFile pf = mileage::read_polyline(in);
  mileage::BvhIndex index(pf.line);
  const mileage::ValidationReport rep = mileage::validate_polyline(pf.line, index);
  std::cout << rep.summary() << "\n";
  for (const auto& issue : rep.issues) {
    std::cout << "  " << mileage::to_string(issue.kind) << " seg_a=" << issue.seg_a
              << " seg_b=" << issue.seg_b << " : " << issue.detail << "\n";
  }
  return rep.ok() ? 0 : 1;
}
