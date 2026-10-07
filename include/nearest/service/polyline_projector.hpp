#pragma once

#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/types.hpp"
#include "nearest/service/configuration.hpp"
#include "nearest/spatial/bvh_index.hpp"

#include <memory>
#include <string>

namespace nearest::service {

struct ProjectorBuildResult {
    FailureCategory category = FailureCategory::None;
    std::string message;
};

class PolylineProjector {
public:
    PolylineProjector() = default;

    ProjectorBuildResult build(
        Polyline polyline,
        const Configuration& configuration);

    [[nodiscard]] QueryOutcome query(const RationalPoint& point) const;
    [[nodiscard]] const Polyline& polyline() const noexcept;
    [[nodiscard]] const Configuration& configuration() const noexcept;
    [[nodiscard]] bool ready() const noexcept;

private:
    Polyline polyline_;
    Configuration configuration_{};
    std::unique_ptr<spatial::BvhIndex> index_;
    bool ready_ = false;
};

}  // namespace nearest::service
