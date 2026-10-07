#pragma once

#include "nearest/geometry/types.hpp"

#include <ostream>
#include <string>

namespace nearest::service {

enum class Decision {
    Accepted,
    Rejected,
    Indeterminate
};

std::string decisionName(Decision decision);
std::string redactIdentifier(const std::string& identifier);

struct DiagnosticRecord {
    std::string request_id;
    Decision decision = Decision::Indeterminate;
    FailureCategory category = FailureCategory::None;
    std::size_t candidate_count = 0;
    std::size_t nodes_visited = 0;
    std::size_t nodes_pruned = 0;
    std::size_t leaves_visited = 0;
    std::string reason;
};

void writeDiagnostic(std::ostream& output, const DiagnosticRecord& record);

}  // namespace nearest::service
