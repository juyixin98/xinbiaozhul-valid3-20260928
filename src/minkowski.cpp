#include "minkowski/minkowski.h"

#include "minkowski/convex_set.h"
#include "minkowski/predicates.h"

#include <algorithm>
#include <stdexcept>
#include <utility>
#include <vector>

namespace minkowski {
namespace {

struct DirectedCycle {
    Point base;
    std::vector<Point> edges;
};

DirectedCycle directed_cycle(const ConvexSet& set) {
    if (set.kind == ShapeKind::Empty || set.vertices.empty()) {
        throw std::invalid_argument("Minkowski input set is empty");
    }
    if (set.kind == ShapeKind::Point) {
        return {set.vertices.front(), {}};
    }

    std::vector<Point> edges;
    Point base;
    if (set.kind == ShapeKind::Segment) {
        const Point forward = set.vertices[1] - set.vertices[0];
        if (edge_angle_less(forward, Point{} - forward)) {
            base = set.vertices[0];
            edges = {forward, Point{} - forward};
        } else {
            base = set.vertices[1];
            edges = {Point{} - forward, forward};
        }
        return {base, edges};
    }

    const std::size_t n = set.vertices.size();
    edges.reserve(n);
    for (std::size_t i = 0; i < n; ++i) {
        edges.push_back(set.vertices[(i + 1) % n] - set.vertices[i]);
    }
    const auto first = std::min_element(edges.begin(), edges.end(),
                                        edge_angle_less);
    const auto offset = static_cast<std::size_t>(
        std::distance(edges.begin(), first));
    std::rotate(edges.begin(), first, edges.end());
    base = set.vertices[offset];
    return {base, std::move(edges)};
}

std::vector<Point> merge_directions(std::vector<Point> edges) {
    std::vector<Point> merged;
    for (Point edge : edges) {
        if (!merged.empty() && same_direction(merged.back(), edge)) {
            merged.back() += edge;
        } else {
            merged.push_back(std::move(edge));
        }
    }
    if (merged.size() > 1 && same_direction(merged.back(), merged.front())) {
        merged.front() += merged.back();
        merged.pop_back();
    }
    return merged;
}

ConvexSet build_from_cycle(Point base, const std::vector<Point>& edges) {
    if (edges.empty()) {
        return make_point(std::move(base));
    }

    std::vector<Point> trail;
    trail.reserve(edges.size());
    Point current = base;
    trail.push_back(current);
    for (const Point& edge : edges) {
        current += edge;
        trail.push_back(current);
    }
    trail.pop_back();

    if (edges.size() == 1) {
        return make_segment(base, base + edges.front());
    }
    const bool all_collinear = std::all_of(edges.begin(), edges.end(),
        [&](const Point& edge) {
            return edges.front() == edge || cross(edges.front(), edge) == 0;
        });
    if (all_collinear) {
        const auto endpoints = std::minmax_element(trail.begin(), trail.end(),
                                                   lex_less);
        return make_segment(*endpoints.first, *endpoints.second);
    }

    const auto it = std::min_element(trail.begin(), trail.end(), lex_less);
    std::rotate(trail.begin(), it, trail.end());
    return {ShapeKind::Polygon, std::move(trail)};
}

} // namespace

ConvexSet minkowski_sum(const ConvexSet& a, const ConvexSet& b) {
    if (a.kind == ShapeKind::Empty || b.kind == ShapeKind::Empty) {
        return {};
    }

    const DirectedCycle ca = directed_cycle(a);
    const DirectedCycle cb = directed_cycle(b);

    std::vector<Point> edges;
    edges.reserve(ca.edges.size() + cb.edges.size());
    std::merge(ca.edges.begin(), ca.edges.end(),
               cb.edges.begin(), cb.edges.end(),
               std::back_inserter(edges), edge_angle_less);
    edges = merge_directions(std::move(edges));
    return build_from_cycle(ca.base + cb.base, edges);
}

ConvexSet translate(const ConvexSet& set, const Point& offset) {
    if (set.kind == ShapeKind::Empty) return {};
    ConvexSet result = set;
    for (Point& vertex : result.vertices) vertex += offset;
    return result;
}

} // namespace minkowski
