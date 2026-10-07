#pragma once

#include "mer/algorithm.hpp"
#include "mer/errors.hpp"
#include "mer/types.hpp"

#include <cstddef>
#include <istream>
#include <ostream>
#include <string>
#include <vector>

namespace mer {

struct BatchOptions {
  std::size_t max_points_per_case = 1'000'000;
  std::size_t max_cases = 100'000;
  bool retain_edge_log = true;
};

struct BatchFailure {
  std::int64_t run_number = -1;
  std::string case_id;
  std::size_t line_number = 0;
  ErrorCategory category = ErrorCategory::ComputationFailed;
  std::string code;
  std::string message;
};

struct BatchSummary {
  std::size_t case_count = 0;
  std::size_t success_count = 0;
  std::size_t failure_count = 0;
  std::vector<BatchFailure> failures;
};

BatchSummary run_batch(std::istream& input,
                       std::ostream& output,
                       const BatchOptions& options = {});

}  // namespace mer
