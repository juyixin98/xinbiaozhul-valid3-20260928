#include "nearest/service/diagnostics.hpp"

#include <iomanip>

namespace nearest::service {

std::string decisionName(Decision decision) {
    switch (decision) {
        case Decision::Accepted:
            return "ACCEPTED";
        case Decision::Rejected:
            return "REJECTED";
        case Decision::Indeterminate:
            return "INDETERMINATE";
    }
    return "INDETERMINATE";
}

std::string redactIdentifier(const std::string& identifier) {
    if (identifier.empty()) {
        return "<missing>";
    }
    if (identifier.size() <= 4U) {
        return std::string(identifier.size(), '*');
    }
    return identifier.substr(0, 2) +
        std::string(identifier.size() - 4U, '*') +
        identifier.substr(identifier.size() - 2U);
}

void writeDiagnostic(std::ostream& output, const DiagnosticRecord& record) {
    output << "timestamp=batch request_id=" << redactIdentifier(record.request_id)
           << " decision=" << decisionName(record.decision)
           << " category=" << failureName(record.category)
           << " candidates=" << record.candidate_count
           << " nodes_visited=" << record.nodes_visited
           << " nodes_pruned=" << record.nodes_pruned
           << " leaves_visited=" << record.leaves_visited
           << " reason=" << std::quoted(record.reason)
           << '\n';
}

}  // namespace nearest::service
