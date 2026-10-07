#pragma once

#include <stdexcept>
#include <string>

namespace mer {

enum class ErrorCategory {
  Success,
  InputError,
  StateConflict,
  ResourceExhausted,
  ComputationFailed,
};

std::string error_category_name(ErrorCategory category);

class MerError : public std::runtime_error {
 public:
  MerError(ErrorCategory category, std::string code, std::string message);

  ErrorCategory category() const noexcept;
  const std::string& code() const noexcept;

 private:
  ErrorCategory category_;
  std::string code_;
};

}  // namespace mer
