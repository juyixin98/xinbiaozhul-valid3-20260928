#include "mbr/enclosing_rectangle.hpp"

#include <algorithm>
#include <cmath>
#include <limits>
#include <stdexcept>

namespace mbr {
namespace {

struct ProjectionSummary {
    Rational dot_min;
    Rational dot_max;
    Rational cross_min;
    Rational cross_max;
    ContactSides contacts;
};

void append_contacts(std::vector<std::string>& target,
                     const std::vector<std::string>& ids) {
    target.insert(target.end(), ids.begin(), ids.end());
}

void sort_ids(std::vector<std::string>& ids) {
    std::sort(ids.begin(), ids.end());
}

ProjectionSummary project_points(const std::vector<HullPoint>& points,
                                  const Point& direction) {
    ProjectionSummary summary;
    bool initialized = false;
    for (const HullPoint& point : points) {
        Rational dot_value = point.coordinate.x * direction.x +
                             point.coordinate.y * direction.y;
        Rational cross_value = point.coordinate.x * direction.y -
                               point.coordinate.y * direction.x;
        if (!initialized) {
            summary.dot_min = summary.dot_max = dot_value;
            summary.cross_min = summary.cross_max = cross_value;
            initialized = true;
        }

        if (dot_value < summary.dot_min) {
            summary.dot_min = dot_value;
            summary.contacts.t_min.clear();
        }
        if (dot_value > summary.dot_max) {
            summary.dot_max = dot_value;
            summary.contacts.t_max.clear();
        }
        if (cross_value < summary.cross_min) {
            summary.cross_min = cross_value;
            summary.contacts.n_max.clear();
        }
        if (cross_value > summary.cross_max) {
            summary.cross_max = cross_value;
            summary.contacts.n_min.clear();
        }

        if (dot_value == summary.dot_min) {
            append_contacts(summary.contacts.t_min, point.source_ids);
        }
        if (dot_value == summary.dot_max) {
            append_contacts(summary.contacts.t_max, point.source_ids);
        }
 // cross(p,d) = -dot(p,left_normal); hence cross maximum is n minimum.
        if (cross_value == summary.cross_max) {
            append_contacts(summary.contacts.n_min, point.source_ids);
        }
        if (cross_value == summary.cross_min) {
            append_contacts(summary.contacts.n_max, point.source_ids);
        }
    }

    sort_ids(summary.contacts.t_min);
    sort_ids(summary.contacts.t_max);
    sort_ids(summary.contacts.n_min);
    sort_ids(summary.contacts.n_max);
    return summary;
}

Point reconstruct_corner(const Point& direction, const Rational& length_squared,
                         const Rational& dot_value,
                         const Rational& left_normal_projection) {
    // q = (dot(q,d) * d + dot(q,n_raw) * n_raw) / |d|^2
    Rational x = dot_value * direction.x +
                 left_normal_projection * (-direction.y);
    Rational y = dot_value * direction.y +
                 left_normal_projection * direction.x;
    return {x / length_squared, y / length_squared};
}

void populate_error_bounds(RectangleSolution& solution, int decimals) {
    Rational delta(1, [&] {
        Integer denominator = 1;
        for (int i = 0; i < decimals; ++i) denominator *= 10;
        return denominator;
    }());
    Rational half_delta = delta / Rational(2);
    Rational conservative_sqrt2 = upper_sqrt(Rational(2));
    Rational coordinate_delta = half_delta;
    Rational corner_area_error =
        Rational(4) * conservative_sqrt2 * delta *
            (solution.side_along + solution.side_perpendicular) +
        Rational(8) * delta.square();
    Rational reported_area_rounding =
        (solution.side_along + solution.side_perpendicular) * half_delta +
        half_delta.square();
    Rational side_length_error =
        Rational(2) * conservative_sqrt2 * delta + half_delta;
    Rational combined =
        (solution.side_along + solution.side_perpendicular) *
            side_length_error +
        side_length_error.square();

    solution.coordinate_error_bound = coordinate_delta.to_fixed(decimals);
    solution.area_rounding_error_bound = Rational(0).to_fixed_upper(decimals);
    solution.reconstructed_rectangle_area_error_bound =
        corner_area_error.to_fixed_upper(decimals);
    solution.combined_area_error_bound =
        corner_area_error.to_fixed_upper(decimals);
    solution.output_decimals = decimals;
    (void)reported_area_rounding;
    (void)combined;
}

RectangleSolution point_solution(std::string run_id,
                                 std::size_t input_point_count,
                                 HullBuild build,
                                 const SolverOptions& options) {
    RectangleSolution solution;
    solution.run_id = std::move(run_id);
    solution.input_point_count = input_point_count;
    solution.unique_point_count = build.unique_points.size();
    solution.hull_vertex_count = 1;
    solution.degenerate_kind = "single_point";
    solution.direction = {Rational(1), Rational(0)};
    solution.left_normal = {Rational(0), Rational(1)};
    Point p = build.hull[0].coordinate;
    solution.corners = {p, p, p, p};
    solution.area = Rational(0);
    solution.side_along = Rational(0);
    solution.side_perpendicular = Rational(0);
    solution.perimeter = Rational(0);
    solution.hull = std::move(build.hull);
    append_contacts(solution.contacts.t_min,
                    build.unique_points[0].source_ids);
    solution.contacts.t_max = solution.contacts.n_min =
        solution.contacts.n_max = solution.contacts.t_min;
    solution.output_decimals = options.output_decimals;
    solution.output_decimals = options.output_decimals;
    populate_error_bounds(solution, options.output_decimals);
    return solution;
}

}  // namespace

RectangleSolution minimum_bounding_rectangle(
    std::string run_id,
    std::size_t input_point_count,
    HullBuild build,
    const SolverOptions& options) {
    if (build.unique_points.empty()) {
        throw std::invalid_argument("point set is empty");
    }
    if (build.hull.size() == 1) {
        return point_solution(std::move(run_id), input_point_count,
                              std::move(build), options);
    }

    RectangleSolution solution;
    solution.run_id = std::move(run_id);
    solution.input_point_count = input_point_count;
    solution.unique_point_count = build.unique_points.size();
    solution.hull = build.hull;
    solution.hull_vertex_count = build.hull.size();
    solution.degenerate_kind = build.hull.size() == 2 ? "collinear" : "none";

    struct Candidate {
        long long index;
        Point start;
        Point end;
        Point direction;
        Rational length_squared;
        Rational area;
        Rational perimeter_comparator;
        Rational dot_range;
        Rational cross_range;
        ProjectionSummary projection;
    };

    std::vector<Candidate> candidates;
    const std::size_t edge_count = build.hull.size() == 2
        ? std::size_t{1}
        : build.hull.size();
    candidates.reserve(edge_count);

    for (std::size_t i = 0; i < edge_count; ++i) {
        const HullPoint& start = build.hull[i];
        const HullPoint& end =
            build.hull[(i + 1) % build.hull.size()];
        Point direction = end.coordinate - start.coordinate;
        Rational length_squared =
            direction.x.square() + direction.y.square();
        if (length_squared.sign() == 0) {
            throw std::runtime_error("zero-length convex hull edge");
        }
        ProjectionSummary projection =
            project_points(build.unique_points, direction);
        Rational dot_range = projection.dot_max - projection.dot_min;
        Rational cross_range = projection.cross_max - projection.cross_min;
        Rational area = dot_range * cross_range / length_squared;
        Rational perimeter_comparator =
            (dot_range + cross_range).square() / length_squared;
        candidates.push_back(Candidate{
            static_cast<long long>(i), start.coordinate, end.coordinate,
            direction, length_squared, area, perimeter_comparator,
            dot_range, cross_range, std::move(projection)});
    }

    const Candidate* best = &candidates.front();
    for (const Candidate& candidate : candidates) {
        bool better =
            candidate.area < best->area ||
            (candidate.area == best->area &&
             (candidate.perimeter_comparator < best->perimeter_comparator ||
              (candidate.perimeter_comparator == best->perimeter_comparator &&
               candidate.index < best->index)));
        if (better) {
            best = &candidate;
        }
    }

    solution.selected_edge_index = best->index;
    solution.direction = best->direction;
    solution.left_normal = {-best->direction.y, best->direction.x};
    solution.area = best->area;
    solution.side_along = best->dot_range / upper_sqrt(best->length_squared);
    solution.side_perpendicular =
        best->cross_range / upper_sqrt(best->length_squared);
    solution.perimeter =
        Rational(2) * (best->dot_range + best->cross_range) /
        upper_sqrt(best->length_squared);
    solution.contacts = best->projection.contacts;

    Rational n_min_value = -best->projection.cross_max;
    Rational n_max_value = -best->projection.cross_min;
    solution.corners = {
        reconstruct_corner(best->direction, best->length_squared,
                           best->projection.dot_min, n_min_value),
        reconstruct_corner(best->direction, best->length_squared,
                           best->projection.dot_max, n_min_value),
        reconstruct_corner(best->direction, best->length_squared,
                           best->projection.dot_max, n_max_value),
        reconstruct_corner(best->direction, best->length_squared,
                           best->projection.dot_min, n_max_value)};

    for (const Candidate& candidate : candidates) {
        DirectionEvaluation record;
        record.edge_index = candidate.index;
        record.edge_start = candidate.start;
        record.edge_end = candidate.end;
        record.edge_squared_length = candidate.length_squared;
        record.dot_range = candidate.dot_range;
        record.cross_range = candidate.cross_range;
        record.area = candidate.area;
        record.perimeter_comparator = candidate.perimeter_comparator;
        record.contacts = candidate.projection.contacts;
        record.decision = candidate.index == best->index
            ? "selected"
            : "rejected_by_fixed_priority";
        solution.direction_evaluations.push_back(std::move(record));
    }

    populate_error_bounds(solution, options.output_decimals);
    return solution;
}

}  // namespace mbr
