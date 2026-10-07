#include "mbr/batch.hpp"
#include "mbr/json.hpp"
#include "test_support.hpp"

#include <string>

int main() {
    using namespace mbr;
    SolverOptions defaults;

    auto contains = [](const std::string& haystack,
                       const std::string& needle) {
        return haystack.find(needle) != std::string::npos;
    };

    {
        BatchResult result = process_batch_input(
            R"({"run_id":"bad-number","points":[{"id":"a","x":"nope","y":"0"}]})",
            defaults);
        CHECK(result.output_lines.size() == 1);
        CHECK(contains(result.output_lines[0], "invalid_input"));
        CHECK(contains(result.output_lines[0], "bad-number"));
    }

    {
        BatchResult result = process_batch_input(
            R"({"run_id":"empty","points":[]})", defaults);
        CHECK(contains(result.output_lines[0], "invalid_input"));
        CHECK(contains(result.output_lines[0], "at least one point"));
    }

    {
        BatchResult result = process_batch_input(
            R"({"run_id":"duplicate","max_input_points":2,"points":[{"id":"x","x":"0","y":"0"},{"id":"x","x":"1","y":"1"}]})",
            defaults);
        CHECK(contains(result.output_lines[0], "state_conflict"));
        CHECK(contains(result.output_lines[0], "duplicate point id"));
    }

    {
        BatchResult result = process_batch_input(
            R"({"run_id":"too-many","max_input_points":1,"points":[{"id":"a","x":"0","y":"0"},{"id":"b","x":"1","y":"1"}]})",
            defaults);
        CHECK(contains(result.output_lines[0], "resource_exhausted"));
        CHECK(contains(result.output_lines[0], "exceeds limit"));
    }

    {
        BatchResult result = process_batch_input("{not json", defaults);
        CHECK(contains(result.output_lines[0], "invalid_input"));
        CHECK(contains(result.output_lines[0], "line_1"));
        CHECK(contains(result.output_lines[0], "json_parse"));
    }

    {
        BatchResult result = process_batch_input(
            R"({"run_id":"ok","points":[{"id":"a","x":"0","y":"0"},{"id":"b","x":"1","y":"0"}]})",
            defaults);
        CHECK(contains(result.output_lines[0], "\"status\":\"success\""));
        CHECK(contains(result.output_lines[0], "\"degenerate_kind\":\"collinear\""));
        CHECK(contains(result.output_lines[0], "\"edge_index\":0"));
    }
    return 0;
}
