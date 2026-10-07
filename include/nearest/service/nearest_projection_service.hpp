#pragma once

#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/types.hpp"
#include "nearest/service/configuration.hpp"
#include "nearest/service/diagnostics.hpp"
#include "nearest/service/polyline_projector.hpp"

#include <optional>
#include <ostream>
#include <string>
#include <vector>

namespace nearest::service {

struct QueryRequest {
    std::string request_id;
    RationalPoint point{};
};

struct LocatedCandidate {
    ProjectionCandidate projection;
    MileageResult mileage;
};

struct RequestResult {
    QueryRequest request;
    Decision decision = Decision::Indeterminate;
    FailureCategory category = FailureCategory::None;
    std::string message;
    std::vector<LocatedCandidate> candidates;
    std::size_t nodes_visited = 0;
    std::size_t nodes_pruned = 0;
    std::size_t leaves_visited = 0;
};

class NearestProjectionService {
public:
    NearestProjectionService(PolylineProjector projector, Configuration configuration);

    RequestResult process(const QueryRequest& request) const;
    void emitResult(std::ostream& output, const RequestResult& result) const;

private:
    PolylineProjector projector_;
    Configuration configuration_;
};

}  // namespace nearest::service
