#include "mbr/batch.hpp"

#include <sstream>

namespace mbr {
namespace {

std::string quote(std::string_view value) {
    return "\"" + json_escape(value) + "\"";
}

std::string json_string_array(const std::vector<std::string>& values) {
    std::string result = "[";
    for (std::size_t i = 0; i < values.size(); ++i) {
        if (i) result.push_back(',');
        result += quote(values[i]);
    }
    result.push_back(']');
    return result;
}

std::string point_decimal(const Point& point, int decimals) {
    return "{\"x\":" + quote(point.x.to_fixed(decimals)) +
           ",\"y\":" + quote(point.y.to_fixed(decimals)) + "}";
}

std::string point_exact(const Point& point) {
    return "{\"x\":" + quote(point.x.to_string()) +
           ",\"y\":" + quote(point.y.to_string()) + "}";
}

std::string contact_json(const ContactSides& contacts) {
    return "{\"tangent_min\":" + json_string_array(contacts.t_min) +
           ",\"tangent_max\":" + json_string_array(contacts.t_max) +
           ",\"normal_min\":" + json_string_array(contacts.n_min) +
           ",\"normal_max\":" + json_string_array(contacts.n_max) + "}";
}

const JsonValue& require(const JsonValue* value, const char* key) {
    if (value == nullptr || value->type != JsonValue::Type::Object) {
        throw std::invalid_argument(std::string("missing object field ") + key);
    }
    const JsonValue* found = json_find(*value, key);
    if (found == nullptr) {
        throw std::invalid_argument(std::string("missing field ") + key);
    }
    return *found;
}

Rational require_rational(const JsonValue& value, const char* key) {
    const JsonValue& field = require(&value, key);
    if (field.type != JsonValue::Type::Number &&
        field.type != JsonValue::Type::String) {
        throw std::invalid_argument(std::string("field ") + key +
                                    " must be number or string");
    }
    auto parsed = Rational::parse(field.string_value);
    if (!parsed) {
        throw std::invalid_argument(std::string("field ") + key +
                                    " is not a finite rational number");
    }
    return *parsed;
}

std::string optional_string(const JsonValue& value, const char* key,
                            const std::string& fallback) {
    const JsonValue* found = json_find(value, key);
    if (found == nullptr) return fallback;
    if (found->type != JsonValue::Type::String) {
        throw std::invalid_argument(std::string("field ") + key +
                                    " must be string");
    }
    return found->string_value;
}

InputPoint parse_point(const JsonValue& value, std::size_t index) {
    if (value.type != JsonValue::Type::Object) {
        throw std::invalid_argument(
            "point at index " + std::to_string(index) +
            " must be an object with x and y");
    }
    InputPoint point;
    point.id = optional_string(value, "id", "p" + std::to_string(index));
    point.x = require_rational(value, "x");
    point.y = require_rational(value, "y");
    return point;
}

std::vector<InputPoint> parse_points(const JsonValue& value) {
    const JsonValue& points = require(&value, "points");
    if (points.type != JsonValue::Type::Array) {
        throw std::invalid_argument("field points must be an array");
    }
    std::vector<InputPoint> result;
    result.reserve(points.array.size());
    for (std::size_t i = 0; i < points.array.size(); ++i) {
        result.push_back(parse_point(points.array[i], i));
    }
    return result;
}

std::string diagnostic_json(const Diagnostic& diagnostic) {
    return "{\"run_id\":" + quote(diagnostic.run_id) +
           ",\"stage\":" + quote(diagnostic.stage) +
           ",\"edge_index\":" + std::to_string(diagnostic.edge_index) +
           ",\"event\":" + quote(diagnostic.event) +
           ",\"detail\":" + quote(diagnostic.detail) + "}";
}

std::string diagnostics_json(const std::vector<Diagnostic>& diagnostics) {
    std::string result = "[";
    for (std::size_t i = 0; i < diagnostics.size(); ++i) {
        if (i) result.push_back(',');
        result += diagnostic_json(diagnostics[i]);
    }
    result.push_back(']');
    return result;
}

}  // namespace

BatchParseOutcome parse_batch_record(const JsonValue& value,
                                     const SolverOptions& defaults) {
    BatchParseOutcome outcome;
    outcome.options = defaults;
    outcome.run_id = optional_string(value, "run_id", "run");
    outcome.points = parse_points(value);
    const JsonValue* tolerance = json_find(value, "near_collinear_tolerance");
    if (tolerance != nullptr) {
        if (tolerance->type != JsonValue::Type::Number &&
            tolerance->type != JsonValue::Type::String) {
            throw std::invalid_argument(
                "near_collinear_tolerance must be number or string");
        }
        auto parsed = Rational::parse(tolerance->string_value);
        if (!parsed) {
            throw std::invalid_argument(
                "near_collinear_tolerance is not finite rational");
        }
        outcome.options.near_collinear_tolerance = *parsed;
    }
    const JsonValue* max_points = json_find(value, "max_input_points");
    if (max_points != nullptr) {
        if (max_points->type != JsonValue::Type::Number ||
            max_points->string_value.find('.') != std::string::npos ||
            max_points->string_value.find('-') != std::string::npos) {
            throw std::invalid_argument("max_input_points must be non-negative integer");
        }
        try {
            unsigned long long parsed =
                std::stoull(max_points->string_value);
            if (parsed > static_cast<unsigned long long>(
                    std::numeric_limits<std::size_t>::max())) {
                throw std::out_of_range("max_input_points too large");
            }
            outcome.options.max_input_points = static_cast<std::size_t>(parsed);
        } catch (const std::exception&) {
            throw std::invalid_argument("invalid max_input_points");
        }
    }
    const JsonValue* decimals = json_find(value, "output_decimals");
    if (decimals != nullptr) {
        if (decimals->type != JsonValue::Type::Number) {
            throw std::invalid_argument("output_decimals must be integer");
        }
        try {
            outcome.options.output_decimals =
                std::stoi(decimals->string_value);
        } catch (const std::exception&) {
            throw std::invalid_argument("invalid output_decimals");
        }
    }
    return outcome;
}

JsonValue failure_to_json(const std::string& run_id, const Status& status,
                          const std::vector<Diagnostic>& diagnostics) {
    std::string body =
        "{\"run_id\":" + quote(run_id) +
        ",\"status\":\"failure\"" +
        ",\"failure_category\":" + quote(failure_category_name(status.category)) +
        ",\"message\":" + quote(status.message) +
        ",\"diagnostics\":" + diagnostics_json(diagnostics) + "}";
    return JsonValue::make_string(std::move(body));
}

JsonValue solution_to_json(const RectangleSolution& solution) {
    std::string corners = "[";
    for (std::size_t i = 0; i < solution.corners.size(); ++i) {
        if (i) corners.push_back(',');
        corners += point_decimal(solution.corners[i],
                                 solution.output_decimals);
    }
    corners.push_back(']');

    std::string hull = "[";
    for (std::size_t i = 0; i < solution.hull.size(); ++i) {
        if (i) hull.push_back(',');
        hull += "{\"coordinate\":" + point_exact(solution.hull[i].coordinate) +
                ",\"source_ids\":" +
                json_string_array(solution.hull[i].source_ids) + "}";
    }
    hull.push_back(']');

    std::string evaluations = "[";
    for (std::size_t i = 0; i < solution.direction_evaluations.size(); ++i) {
        const DirectionEvaluation& record = solution.direction_evaluations[i];
        if (i) evaluations.push_back(',');
        evaluations +=
            "{\"edge_index\":" + std::to_string(record.edge_index) +
            ",\"edge_start\":" + point_exact(record.edge_start) +
            ",\"edge_end\":" + point_exact(record.edge_end) +
            ",\"edge_squared_length\":\"" +
            record.edge_squared_length.to_string() + "\"" +
            ",\"dot_range\":\"" + record.dot_range.to_string() + "\"" +
            ",\"cross_range\":\"" + record.cross_range.to_string() + "\"" +
            ",\"area\":\"" + record.area.to_string() + "\"" +
            ",\"perimeter_comparator\":\"" +
            record.perimeter_comparator.to_string() + "\"" +
            ",\"contacts\":" + contact_json(record.contacts) +
            ",\"decision\":" + quote(record.decision) + "}";
    }
    evaluations.push_back(']');

    std::string observations = "[";
    for (std::size_t i = 0; i < solution.near_collinear_observations.size();
         ++i) {
        if (i) observations.push_back(',');
        const NearCollinearObservation& obs =
            solution.near_collinear_observations[i];
        observations += "{\"a\":" + point_exact(obs.a) +
                        ",\"b\":" + point_exact(obs.b) +
                        ",\"c\":" + point_exact(obs.c) +
                        ",\"near_collinear\":" +
                        std::string(obs.near_collinear ? "true" : "false") +
                        ",\"zero_reference_segment\":" +
                        std::string(obs.zero_reference_segment ? "true"
                                                               : "false") +
                        "}";
    }
    observations.push_back(']');

    std::string body =
        "{\"run_id\":" + quote(solution.run_id) +
        ",\"status\":\"success\","
        "\"input_point_count\":" +
        std::to_string(solution.input_point_count) +
        ",\"unique_point_count\":" +
        std::to_string(solution.unique_point_count) +
        ",\"hull_vertex_count\":" +
        std::to_string(solution.hull_vertex_count) +
        ",\"degenerate_kind\":" + quote(solution.degenerate_kind) +
        ",\"selected_edge_index\":" +
        std::to_string(solution.selected_edge_index) +
        ",\"direction\":" + point_exact(solution.direction) +
        ",\"left_normal\":" + point_exact(solution.left_normal) +
        ",\"corners\":" + corners +
        ",\"contacts\":" + contact_json(solution.contacts) +
        ",\"area\":\"" + solution.area.to_string() + "\"" +
        ",\"side_along\":\"" + solution.side_along.to_string() + "\"" +
        ",\"side_perpendicular\":\"" +
        solution.side_perpendicular.to_string() + "\"" +
        ",\"perimeter\":\"" + solution.perimeter.to_string() + "\"" +
        ",\"hull\":" + hull +
        ",\"direction_evaluations\":" + evaluations +
        ",\"near_collinear_observations\":" + observations +
        ",\"error_bounds\":{"
        "\"coordinate_absolute_error\":" +
        quote(solution.coordinate_error_bound) +
        ",\"reported_area_rounding_error\":" +
        quote(solution.area_rounding_error_bound) +
        ",\"reconstructed_rectangle_area_error\":" +
        quote(solution.reconstructed_rectangle_area_error_bound) +
        ",\"combined_area_error\":" +
        quote(solution.combined_area_error_bound) + "}}";
    return JsonValue::make_string(std::move(body));
}

BatchResult process_batch_input(std::string_view text,
                                const SolverOptions& default_options) {
    BatchResult batch;
    std::string input(text);
    std::stringstream stream(std::move(input));
    std::string line;
    std::size_t line_number = 0;
    while (std::getline(stream, line)) {
        ++line_number;
        std::size_t first = line.find_first_not_of(" \t\r\n");
        if (first == std::string::npos) continue;
        std::string run_id = "line_" + std::to_string(line_number);
        JsonParseResult parsed = parse_json(line);
        if (!parsed.ok) {
            Status status{FailureCategory::InvalidInput, parsed.error};
            std::vector<Diagnostic> diagnostics{Diagnostic{
                .run_id = run_id,
                .stage = "json_parse",
                .edge_index = -1,
                .event = "failure",
                .detail = parsed.error}};
            batch.output_lines.push_back(
                failure_to_json(run_id, status, diagnostics).string_value);
            continue;
        }

        BatchParseOutcome record;
        record.options = default_options;
        try {
            BatchParseOutcome parsed_record =
                parse_batch_record(parsed.value, default_options);
            record = std::move(parsed_record);
        } catch (const std::exception& error) {
            Status status{FailureCategory::InvalidInput, error.what()};
            std::vector<Diagnostic> diagnostics{Diagnostic{
                .run_id = run_id,
                .stage = "record_parse",
                .edge_index = -1,
                .event = "failure",
                .detail = error.what()}};
            batch.output_lines.push_back(
                failure_to_json(run_id, status, diagnostics).string_value);
            continue;
        }

        SolveResult solved = solve_minimum_bounding_rectangle(
            record.run_id, record.points, record.options);
        if (!solved.status.ok()) {
            batch.output_lines.push_back(failure_to_json(
                record.run_id, solved.status, solved.diagnostics).string_value);
            continue;
        }
        batch.output_lines.push_back(
            solution_to_json(*solved.solution).string_value);
    }
    return batch;
}

}  // namespace mbr
