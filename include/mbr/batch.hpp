#pragma once

#include "mbr/json.hpp"
#include "mbr/solver.hpp"

#include <string>

namespace mbr {

struct BatchResult {
    Status status;
    std::vector<std::string> output_lines;
};

struct BatchParseOutcome {
    Status status;
    std::string run_id;
    std::vector<InputPoint> points;
    SolverOptions options;
};

BatchParseOutcome parse_batch_record(
    const JsonValue& value,
    const SolverOptions& defaults = SolverOptions{});
JsonValue solution_to_json(const RectangleSolution& solution);
JsonValue failure_to_json(const std::string& run_id, const Status& status,
                          const std::vector<Diagnostic>& diagnostics);
BatchResult process_batch_input(std::string_view text,
                                const SolverOptions& default_options);

}  // namespace mbr
