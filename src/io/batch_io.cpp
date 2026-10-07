#include "nearest/io/batch_io.hpp"

#include "nearest/geometry/decimal.hpp"

#include <sstream>

namespace nearest::io {

namespace {

std::vector<std::string> splitCsv(const std::string& line) {
    std::vector<std::string> fields;
    std::string current;
    std::istringstream row(line);
    while (std::getline(row, current, ',')) {
        fields.push_back(current);
    }
    return fields;
}

}  // namespace

BatchParseResult parseBatchInput(std::istream& input) {
    BatchParseResult result;
    std::string line;
    std::size_t line_number = 0;

    while (std::getline(input, line)) {
        ++line_number;
        if (!line.empty() && line.back() == '\r') {
            line.pop_back();
        }
        if (line.empty() || line[0] == '#') {
            continue;
        }

        std::vector<std::string> fields = splitCsv(line);
        if (fields.empty()) {
            continue;
        }

        if (fields[0] == "V") {
            if (fields.size() != 3U) {
                result.failure = {line_number, FailureCategory::InvalidVertexRecord,
                    "vertex record must be V,x,y"};
                return result;
            }
            auto x = parseDecimalRational(fields[1]);
            auto y = parseDecimalRational(fields[2]);
            if (!x.has_value() || !y.has_value()) {
                result.failure = {line_number, FailureCategory::InvalidCoordinate,
                    "vertex x or y is not a finite decimal"};
                return result;
            }
            result.data.polyline.vertices.push_back({*x, *y});
        } else if (fields[0] == "Q") {
            if (fields.size() != 4U) {
                result.failure = {line_number, FailureCategory::InvalidQueryRecord,
                    "query record must be Q,id,x,y"};
                return result;
            }
            if (fields[1].empty()) {
                result.failure = {line_number, FailureCategory::InvalidQueryRecord,
                    "query id is required"};
                return result;
            }
            auto x = parseDecimalRational(fields[2]);
            auto y = parseDecimalRational(fields[3]);
            if (!x.has_value() || !y.has_value()) {
                result.failure = {line_number, FailureCategory::InvalidCoordinate,
                    "query x or y is not a finite decimal"};
                return result;
            }
            result.data.requests.push_back({fields[1], {*x, *y}});
        } else {
            result.failure = {line_number, FailureCategory::InvalidQueryRecord,
                "record type must be V or Q"};
            return result;
        }
    }

    result.ok = true;
    return result;
}

}  // namespace nearest::io
