#pragma once

#include "nearest/geometry/polyline.hpp"
#include "nearest/service/nearest_projection_service.hpp"

#include <iosfwd>
#include <string>
#include <vector>

namespace nearest::io {

struct ParsedBatch {
    Polyline polyline;
    std::vector<service::QueryRequest> requests;
};

struct ParseFailure {
    std::size_t line_number = 0;
    FailureCategory category = FailureCategory::None;
    std::string message;
};

struct BatchParseResult {
    bool ok = false;
    ParsedBatch data;
    ParseFailure failure;
};

BatchParseResult parseBatchInput(std::istream& input);

}  // namespace nearest::io
