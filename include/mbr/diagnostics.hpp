#pragma once

#include <string>
#include <vector>

namespace mbr {

enum class FailureCategory {
    Ok,
    InvalidInput,
    StateConflict,
    ResourceExhausted,
    ComputationFailed
};

std::string failure_category_name(FailureCategory category);

struct Status {
    FailureCategory category = FailureCategory::Ok;
    std::string message;

    [[nodiscard]] bool ok() const noexcept {
        return category == FailureCategory::Ok;
    }
};

struct Diagnostic {
    std::string run_id;
    std::string stage;
    long long edge_index = -1;
    std::string event;
    std::string detail;
};

class DiagnosticLog {
public:
    void add(std::string run_id, std::string stage, std::string event,
             std::string detail, long long edge_index = -1);
    [[nodiscard]] const std::vector<Diagnostic>& entries() const noexcept {
        return entries_;
    }

private:
    std::vector<Diagnostic> entries_;
};

}  // namespace mbr
