#pragma once

#include "nearest/geometry/polyline.hpp"
#include "nearest/geometry/types.hpp"

#include <cstddef>
#include <vector>

namespace nearest::spatial {

struct BoundingBox {
    RationalPoint minimum{};
    RationalPoint maximum{};
};

class BvhIndex {
public:
    explicit BvhIndex(const Polyline& polyline);

    [[nodiscard]] QueryOutcome nearest(const RationalPoint& query) const;
    [[nodiscard]] std::size_t nodeCount() const noexcept;
    [[nodiscard]] std::size_t segmentCount() const noexcept;

    static Rational squaredDistanceToBox(
        const RationalPoint& point,
        const BoundingBox& box);

private:
    struct Node {
        BoundingBox bounds{};
        std::size_t left = 0;
        std::size_t right = 0;
        std::size_t first_leaf = 0;
        std::size_t leaf_count = 0;
        bool leaf = false;
    };

    struct Leaf {
        BoundingBox bounds{};
        RationalPoint centroid{};
        std::size_t segment_index = 0;
    };

    std::size_t build(std::vector<Leaf>& leaves, std::size_t first, std::size_t last);
    std::size_t build();
    void traverse(
        std::size_t node_id,
        const RationalPoint& query,
        std::vector<ProjectionCandidate>& best,
        QueryOutcome& counters) const;

    const Polyline* polyline_ = nullptr;
    std::vector<Node> nodes_;
    std::vector<Leaf> leaves_;
};

}  // namespace nearest::spatial
