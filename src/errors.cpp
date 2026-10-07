#include "mer/errors.hpp"

namespace mer {

std::string error_category_name(ErrorCategory category) {
  switch (category) {
    case ErrorCategory::Success:
      return "SUCCESS";
    case ErrorCategory::InputError:
      return "INPUT_ERROR";
    case ErrorCategory::StateConflict:
      return "STATE_CONFLICT";
    case ErrorCategory::ResourceExhausted:
      return "RESOURCE_EXHAUSTED";
    case ErrorCategory::ComputationFailed:
      return "COMPUTATION_FAILED";
  }
  return "COMPUTATION_FAILED";
}

MerError::MerError(ErrorCategory category, std::string code, std::string message)
    : std::runtime_error(std::move(message)),
      category_(category),
      code_(std::move(code)) {}

ErrorCategory MerError::category() const noexcept { return category_; }
const std::string& MerError::code() const noexcept { return code_; }

}  // namespace mer
