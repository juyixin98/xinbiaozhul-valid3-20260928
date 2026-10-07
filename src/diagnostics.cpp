#include "mbr/diagnostics.hpp"

namespace mbr {

std::string failure_category_name(FailureCategory category) {
    switch (category) {
        case FailureCategory::Ok:
            return "ok";
        case FailureCategory::InvalidInput:
            return "invalid_input";
        case FailureCategory::StateConflict:
            return "state_conflict";
        case FailureCategory::ResourceExhausted:
            return "resource_exhausted";
        case FailureCategory::ComputationFailed:
            return "computation_failed";
    }
    return "computation_failed";
}

void DiagnosticLog::add(std::string run_id, std::string stage,
                        std::string event, std::string detail,
                        long long edge_index) {
    entries_.push_back(Diagnostic{
        std::move(run_id), std::move(stage), edge_index,
        std::move(event), std::move(detail)});
}

}  // namespace mbr
