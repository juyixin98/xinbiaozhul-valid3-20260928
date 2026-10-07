#include "mer/batch.hpp"
#include "mer/errors.hpp"

#include <cstdlib>
#include <fstream>
#include <iostream>
#include <string>

namespace {

void usage(const char* argv0) {
  std::cerr
      << "Usage: " << argv0
      << " [input-file] [--output file] [--no-edge-log] [--max-points N]\n"
      << "Reads newline protocol from a file or stdin and writes JSONL.\n";
}

}  // namespace

int main(int argc, char** argv) {
  std::string input_path;
  std::string output_path;
  mer::BatchOptions options;

  for (int i = 1; i < argc; ++i) {
    std::string arg = argv[i];
    if (arg == "--help" || arg == "-h") {
      usage(argv[0]);
      return 0;
    }
    if (arg == "--output" || arg == "-o") {
      if (++i >= argc) {
        std::cerr << "missing value for --output\n";
        return 2;
      }
      output_path = argv[i];
    } else if (arg == "--no-edge-log") {
      options.retain_edge_log = false;
    } else if (arg == "--max-points") {
      if (++i >= argc) {
        std::cerr << "missing value for --max-points\n";
        return 2;
      }
      options.max_points_per_case = std::strtoull(argv[i], nullptr, 10);
    } else if (!arg.starts_with("--") && input_path.empty()) {
      input_path = arg;
    } else {
      std::cerr << "unknown argument: " << arg << "\n";
      usage(argv[0]);
      return 2;
    }
  }

  std::ifstream input_file;
  std::istream* input = &std::cin;
  if (!input_path.empty()) {
    input_file.open(input_path);
    if (!input_file) {
      std::cerr << "cannot open input file: " << input_path << "\n";
      return 2;
    }
    input = &input_file;
  }

  std::ofstream output_file;
  std::ostream* output = &std::cout;
  if (!output_path.empty()) {
    output_file.open(output_path);
    if (!output_file) {
      std::cerr << "cannot open output file: " << output_path << "\n";
      return 2;
    }
    output = &output_file;
  }

  try {
    mer::BatchSummary summary = mer::run_batch(*input, *output, options);
    std::cerr << "cases=" << summary.case_count
              << " ok=" << summary.success_count
              << " failed=" << summary.failure_count << "\n";
    return summary.failure_count == 0 ? 0 : 1;
  } catch (const std::exception& error) {
    std::cerr << "fatal: " << error.what() << "\n";
    return 3;
  }
}
