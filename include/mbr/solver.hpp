#pragma once

#include "mbr/diagnostics.hpp"
#include "mbr/geometry.hpp"

#include <optional>
#include <string>
#include <vector>

namespace mbr {

struct SolverOptions {
    std::size_t max_input_points = 100000;
    Rational near_collinear_tolerance = Rational(0);
    int output_decimals = 9;
};

struct ContactSides {
    std::vector<std::string> t_min;
    std::vector<std::string> t_max;
    std::vector<std::string> n_min;
    std::vector<std::string> n_max;
};

struct DirectionEvaluation {
    long long edge_index = -1;
    Point edge_start;
    Point edge_end;
    Rational edge_squared_length;
    Rational dot_range;
    Rational cross_range;
    Rational area;
    Rational perimeter_comparator;
    ContactSides contacts;
    std::string decision;
};

struct NearCollinearObservation {
    Point a;
    Point b;
    Point c;
    bool near_collinear = false;
    bool zero_reference_segment = false;
};

struct RectangleSolution {
    std::string run_id;
    std::size_t input_point_count = 0;
    std::size_t unique_point_count = 0;
    std::size_t hull_vertex_count = 0;
    std::string degenerate_kind;
    int output_decimals = 9;
    long long selected_edge_index = -1;
    Point direction;
    Point left_normal;
    std::array<Point, 4> corners{};
    ContactSides contacts;
    Rational area;
    Rational side_along;
    Rational side_perpendicular;
    Rational perimeter;
    std::vector<HullPoint> hull;
    std::vector<DirectionEvaluation> direction_evaluations;
    std::vector<NearCollinearObservation> near_collinear_observations;
    std::string coordinate_error_bound;
    std::string area_rounding_error_bound;
    std::string reconstructed_rectangle_area_error_bound;
    std::string combined_area_error_bound;
};

struct SolveResult {
    Status status;
    std::optional<RectangleSolution> solution;
    std::vector<Diagnostic> diagnostics;
};

Status validate_options(const SolverOptions& options);
SolveResult solve_minimum_bounding_rectangle(
    std::string run_id,
    const std::vector<InputPoint>& points,
    const SolverOptions& options);

}  // namespace mbr
