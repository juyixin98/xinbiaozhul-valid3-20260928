#include "nearest/spatial/bvh_index.hpp"

#include "nearest/geometry/predicates.hpp"

#include <algorithm>
#include <limits>

namespace nearest::spatial {

namespace {

void expand(BoundingBox& box, const RationalPoint& point) {
    box.minimum.x = std::min(box.minimum.x, point.x);
    box.minimum.y = std::min(box.minimum.y, point.y);
    box.maximum.x = std::max(box.maximum.x, point.x);
    box.maximum.y = std::max(box.maximum.y, point.y);
}

BoundingBox boundsFor(const RationalPoint& a, const RationalPoint& b) {
    BoundingBox box{a, a};
    expand(box, b);
    return box;
}

}  // namespace

BvhIndex::BvhIndex(const Polyline& polyline) : polyline_(&polyline) {
    leaves_.reserve(polyline.segmentCount());
    for (std::size_t i = 0U; i < polyline.segmentCount(); ++i) {
        const RationalPoint& a = polyline.vertices[i];
        const RationalPoint& b = polyline.vertices[i + 1U];
        BoundingBox bounds = boundsFor(a, b);
        leaves_.push_back(Leaf{
            bounds,
            {(a.x + b.x) / 2, (a.y + b.y) / 2},
            i
        });
    }

    if (!leaves_.empty()) {
        nodes_.reserve(leaves_.size() * 2U);
        build();
    }
}

std::size_t BvhIndex::build() {
    return build(leaves_, 0U, leaves_.size());
}

std::size_t BvhIndex::build(std::vector<Leaf>& leaves, std::size_t first, std::size_t last) {
    BoundingBox combined = leaves[first].bounds;
    BoundingBox centroid_bounds{leaves[first].centroid, leaves[first].centroid};
    for (std::size_t i = first + 1U; i < last; ++i) {
        expand(combined, leaves[i].bounds.minimum);
        expand(combined, leaves[i].bounds.maximum);
        expand(centroid_bounds, leaves[i].centroid);
    }

    Node node;
    node.bounds = combined;
    if (last - first == 1U) {
        node.leaf = true;
        node.first_leaf = first;
        node.leaf_count = 1U;
        nodes_.push_back(node);
        return nodes_.size() - 1U;
    }

    Rational x_span = centroid_bounds.maximum.x - centroid_bounds.minimum.x;
    Rational y_span = centroid_bounds.maximum.y - centroid_bounds.minimum.y;
    bool split_x = x_span >= y_span;
    std::sort(leaves.begin() + static_cast<std::ptrdiff_t>(first),
        leaves.begin() + static_cast<std::ptrdiff_t>(last),
        [split_x](const Leaf& a, const Leaf& b) {
            if (split_x && a.centroid.x != b.centroid.x) {
                return a.centroid.x < b.centroid.x;
            }
            if (!split_x && a.centroid.y != b.centroid.y) {
                return a.centroid.y < b.centroid.y;
            }
            return a.segment_index < b.segment_index;
        });

    std::size_t middle = first + (last - first) / 2U;
    nodes_.push_back(node);
    std::size_t node_id = nodes_.size() - 1U;
    std::size_t left = build(leaves, first, middle);
    std::size_t right = build(leaves, middle, last);
    nodes_[node_id].left = left;
    nodes_[node_id].right = right;
    return node_id;
}

Rational BvhIndex::squaredDistanceToBox(
    const RationalPoint& point,
    const BoundingBox& box) {
    Rational dx = 0;
    Rational dy = 0;
    if (point.x < box.minimum.x) {
        dx = box.minimum.x - point.x;
    } else if (point.x > box.maximum.x) {
        dx = point.x - box.maximum.x;
    }
    if (point.y < box.minimum.y) {
        dy = box.minimum.y - point.y;
    } else if (point.y > box.maximum.y) {
        dy = point.y - box.maximum.y;
    }
    return dx * dx + dy * dy;
}

QueryOutcome BvhIndex::nearest(const RationalPoint& query) const {
    QueryOutcome counters;
    if (nodes_.empty()) {
        return counters;
    }
    traverse(0U, query, counters.candidates, counters);

    // Traversal can visit an equal-distance right subtree after a closer left
    // subtree; one final index sort gives the documented stable candidate set.
    std::sort(counters.candidates.begin(), counters.candidates.end(),
        [](const ProjectionCandidate& a, const ProjectionCandidate& b) {
            return a.segment_index < b.segment_index;
        });
    return counters;
}

void BvhIndex::traverse(
    std::size_t node_id,
    const RationalPoint& query,
    std::vector<ProjectionCandidate>& best,
    QueryOutcome& counters) const {
    const Node& node = nodes_[node_id];
    ++counters.nodes_visited;

    Rational lower_bound = squaredDistanceToBox(query, node.bounds);
    if (!best.empty() && lower_bound > best.front().distance_squared) {
        ++counters.nodes_pruned;
        return;
    }

    if (node.leaf) {
        ++counters.leaves_visited;
        const Leaf& leaf = leaves_[node.first_leaf];
        ProjectionCandidate candidate = projectToSegment(
            query,
            polyline_->vertices[leaf.segment_index],
            polyline_->vertices[leaf.segment_index + 1U],
            leaf.segment_index);

        if (best.empty() || candidate.distance_squared < best.front().distance_squared) {
            best.clear();
            best.push_back(std::move(candidate));
        } else if (candidate.distance_squared == best.front().distance_squared) {
            best.push_back(std::move(candidate));
        }
        return;
    }

    // Internal children are visited in ascending spatial build order. Equality
    // is deliberately admitted because multiple segments may tie.
    traverse(node.right, query, best, counters);
    traverse(node.left, query, best, counters);
}

std::size_t BvhIndex::nodeCount() const noexcept {
    return nodes_.size();
}

std::size_t BvhIndex::segmentCount() const noexcept {
    return polyline_->segmentCount();
}

}  // namespace nearest::spatial
