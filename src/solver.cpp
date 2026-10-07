#include "mbr/solver.hpp"

#include "mbr/convex_hull.hpp"
#include "mbr/enclosing_rectangle.hpp"
#include "mbr/predicates.hpp"

#include <exception>
#include <limits>
#include <set>

namespace mbr {
namespace {

Point raw_point(const InputPoint& point) {
    return {point.x, point.y};
}

std::vector<NearCollinearObservation> collect_near_turns(
    const std::vector<HullPoint>& hull,
    const Rational& tolerance) {
    std::vector<NearCollinearObservation> observations;
    if (hull.size() < 3 || tolerance.sign() < 0) {
        return observations;
    }
    observations.reserve(hull.size());
    for (std::size_t i = 0; i < hull.size(); ++i) {
        const Point& a = hull[i].coordinate;
        const Point& b = hull[(i + 1) % hull.size()].coordinate;
        const Point& c = hull[(i + 2) % hull.size()].coordinate;
        NearCollinearResult result = near_collinear(a, b, c, tolerance);
        NearCollinearObservation observation;
        observation.a = a;
        observation.b = b;
        observation.c = c;
        observation.near_collinear = result.near_collinear;
        observation.zero_reference_segment =
            result.reference_segment_is_zero_length;
        observations.push_back(observation);
    }
    return observations;
}

}  // namespace

Status validate_options(const SolverOptions& options) {
    if (options.max_input_points == 0) {
        return {FailureCategory::InvalidInput,
                "max_input_points must be positive"};
    }
    if (options.near_collinear_tolerance.sign() < 0) {
        return {FailureCategory::InvalidInput,
                "near_collinear_tolerance must be non-negative"};
    }
    if (options.output_decimals < 0 || options.output_decimals > 1000) {
        return {FailureCategory::InvalidInput,
                "output_decimals must be between 0 and 1000"};
    }
    return {};
}

SolveResult solve_minimum_bounding_rectangle(
    std::string run_id,
    const std::vector<InputPoint>& points,
    const SolverOptions& options) {
    SolveResult result;
    auto fail = [&](FailureCategory category, const std::string& message) {
        result.status = {category, message};
        result.diagnostics.push_back(Diagnostic{
            .run_id = run_id,
            .stage = "solve",
            .edge_index = -1,
            .event = "failure",
            .detail = message});
        return result;
    };

    Status option_status = validate_options(options);
    if (!option_status.ok()) {
        return fail(option_status.category, option_status.message);
    }
    if (points.size() > options.max_input_points) {
        return fail(FailureCategory::ResourceExhausted,
                    "input point count " + std::to_string(points.size()) +
                    " exceeds limit " +
                    std::to_string(options.max_input_points));
    }
    if (points.empty()) {
        return fail(FailureCategory::InvalidInput,
                    "point set must contain at least one point");
    }

    try {
        std::set<std::string> ids;
        std::vector<HullPoint> aggregated;
        aggregated.reserve(points.size());
        for (const InputPoint& point : points) {
            if (point.id.empty()) {
                return fail(FailureCategory::InvalidInput,
                            "point id must not be empty");
            }
            if (!ids.insert(point.id).second) {
                return fail(FailureCategory::StateConflict,
                            "duplicate point id: " + point.id);
            }
            aggregated.push_back(HullPoint{
                raw_point(point), std::vector<std::string>{point.id}});
        }

        HullBuild hull_build = build_convex_hull(std::move(aggregated));
        RectangleSolution solution = minimum_bounding_rectangle(
            run_id, points.size(), std::move(hull_build), options);
        solution.near_collinear_observations =
            collect_near_turns(solution.hull,
                               options.near_collinear_tolerance);
        result.solution = std::move(solution);
        return result;
    } catch (const std::bad_alloc& error) {
        return fail(FailureCategory::ResourceExhausted,
                    std::string("memory exhausted: ") + error.what());
    } catch (const std::invalid_argument& error) {
        return fail(FailureCategory::InvalidInput, error.what());
    } catch (const std::domain_error& error) {
        return fail(FailureCategory::ComputationFailed, error.what());
    } catch (const std::runtime_error& error) {
        return fail(FailureCategory::ComputationFailed, error.what());
    } catch (const std::exception& error) {
        return fail(FailureCategory::ComputationFailed,
                    std::string("unexpected computation error: ") +
                        error.what());
    }
}

}  // namespace mbr
