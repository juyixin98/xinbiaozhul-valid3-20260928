#include "minkowski/convex_set.h"

#include "minkowski/predicates.h"

#include <algorithm>
#include <set>

namespace minkowski {

namespace {

std::vector<Point> rotate_to_min(std::vector<Point> points) {
    const auto it = std::min_element(points.begin(), points.end(), lex_less);
    std::rotate(points.begin(), it, points.end());
    return points;
}

std::vector<Point> remove_collinear_cyclic(std::vector<Point> points) {
    if (points.size() < 3) return points;
    bool changed = true;
    while (changed && points.size() >= 3) {
        changed = false;
        const std::size_t n = points.size();
        for (std::size_t i = 0; i < n; ++i) {
            const Point& prev = points[(i + n - 1) % n];
            const Point& current = points[i];
            const Point& next = points[(i + 1) % n];
            if (cross(prev, current, next) == 0 &&
                same_direction(current - prev, next - current)) {
                points.erase(points.begin() + static_cast<std::ptrdiff_t>(i));
                changed = true;
                break;
            }
        }
    }
    return points;
}

} // namespace

std::string to_string(ValidationError error) {
    switch (error) {
    case ValidationError::EmptyInput:
        return "EMPTY_INPUT";
    case ValidationError::DuplicateVertex:
        return "DUPLICATE_VERTEX";
    case ValidationError::NonConvex:
        return "NON_CONVEX";
    case ValidationError::SelfIntersectionOrRepeatedTraversal:
        return "SELF_INTERSECTION_OR_REPEATED_TRAVERSAL";
    }
    return "UNKNOWN_VALIDATION_ERROR";
}

ValidationResult validate_and_normalize(const std::vector<Point>& input) {
    if (input.empty()) {
        return {false, {}, ValidationError::EmptyInput};
    }

    std::set<Point, decltype([](const Point& a, const Point& b) {
        return lex_less(a, b);
    })> unique_points(input.begin(), input.end());
    if (unique_points.size() != input.size()) {
        return {false, {}, ValidationError::DuplicateVertex};
    }

    if (input.size() == 1) {
        return {true, make_point(input.front()), {}};
    }

    if (input.size() == 2) {
        return {true, make_segment(input[0], input[1]), {}};
    }

    bool has_positive = false;
    bool has_negative = false;
    bool all_collinear = true;

    const std::size_t n = input.size();
    for (std::size_t i = 0; i < n; ++i) {
        const Point& a = input[i];
        const Point& b = input[(i + 1) % n];
        const Point& c = input[(i + 2) % n];
        const Point u = b - a;
        const Point v = c - b;
        const Integer turn = cross(u, v);
        if (turn > 0) {
            has_positive = true;
            all_collinear = false;
        } else if (turn < 0) {
            has_negative = true;
            all_collinear = false;
        } else if (!same_direction(u, v)) {
            // A cyclic traversal of a distinct collinear point set reverses at
            // its two endpoints. It represents a segment, not a polygon.
            continue;
        }
    }

    if (all_collinear) {
        const auto endpoints = std::minmax_element(
            input.begin(), input.end(), lex_less);
        return {true, make_segment(*endpoints.first, *endpoints.second), {}};
    }

    if (has_positive && has_negative) {
        return {false, {}, ValidationError::NonConvex};
    }

    std::vector<Point> ordered = input;
    if (has_negative) std::reverse(ordered.begin(), ordered.end());
    ordered = rotate_to_min(std::move(ordered));
    ordered = remove_collinear_cyclic(std::move(ordered));

    if (ordered.size() == 1) {
        return {true, make_point(ordered.front()), {}};
    }
    if (ordered.size() == 2) {
        return {true, make_segment(ordered[0], ordered[1]), {}};
    }

    ConvexSet result;
    result.kind = ShapeKind::Polygon;
    result.vertices = rotate_to_min(std::move(ordered));
    return {true, std::move(result), {}};
}

ConvexSet make_point(Point p) {
    return {ShapeKind::Point, {std::move(p)}};
}

ConvexSet make_segment(Point a, Point b) {
    if (lex_less(b, a)) std::swap(a, b);
    return {ShapeKind::Segment, {std::move(a), std::move(b)}};
}

bool canonically_equal(const ConvexSet& a, const ConvexSet& b) {
    if (a.kind != b.kind || a.vertices.size() != b.vertices.size()) {
        return false;
    }
    return a.vertices == b.vertices;
}

} // namespace minkowski
