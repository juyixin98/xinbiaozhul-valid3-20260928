#include "mer/batch.hpp"

#include <array>
#include <cerrno>
#include <charconv>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <optional>
#include <sstream>

#include "mer/json.hpp"

namespace mer {

namespace {

std::string hull_kind_name(HullKind kind) {
  switch (kind) {
    case HullKind::Empty:
      return "empty";
    case HullKind::Point:
      return "point";
    case HullKind::Segment:
      return "segment";
    case HullKind::Polygon:
      return "polygon";
  }
  return "unknown";
}

std::string ids_json(const std::vector<std::int64_t>& ids) {
  std::ostringstream out;
  out << "[";
  for (std::size_t i = 0; i < ids.size(); ++i) {
    if (i) out << ",";
    out << ids[i];
  }
  out << "]";
  return out.str();
}

std::string exact_point_json(const ExactPoint& p) {
  std::ostringstream out;
  out << "{\"x\":\"" << to_string(p.x) << "\",\"y\":\"" << to_string(p.y)
      << "\",\"x_decimal\":\"" << json_escape(to_string(p.x.value()))
      << "\",\"y_decimal\":\"" << json_escape(to_string(p.y.value()))
      << "\"}";
  return out.str();
}

std::string point_json(const Point& p) {
  std::ostringstream out;
  out << "{\"id\":" << p.id << ",\"x\":" << p.x << ",\"y\":" << p.y << "}";
  return out.str();
}

void write_failure(std::ostream& output, const BatchFailure& failure) {
  output << "{\"status\":\"error\",\"run_number\":" << failure.run_number
         << ",\"case_id\":\"" << json_escape(failure.case_id)
         << "\",\"line_number\":" << failure.line_number
         << ",\"error_category\":\""
         << error_category_name(failure.category)
         << "\",\"error_code\":\"" << json_escape(failure.code)
         << "\",\"message\":\"" << json_escape(failure.message) << "\"}\n";
}

void write_success(std::ostream& output,
                   std::int64_t run_number,
                   const std::string& case_id,
                   std::size_t line_number,
                   const std::vector<Point>& points,
                   const MinimumRectangle& result,
                   bool include_log) {
  output << "{\"status\":\"ok\",\"run_number\":" << run_number
         << ",\"case_id\":\"" << json_escape(case_id)
         << "\",\"begin_line\":" << line_number
         << ",\"input_point_count\":" << points.size()
         << ",\"hull_kind\":\"" << hull_kind_name(result.hull_kind)
         << "\",\"selected_edge_index\":" << result.selected_edge_index
         << ",\"area\":\"" << to_string(result.area)
         << "\",\"area_decimal\":\""
         << json_escape(to_string(result.area_decimal))
         << "\",\"area_error_bound\":\""
         << json_escape(to_string(result.area_error_bound))
         << "\",\"corners\":[";
  for (std::size_t i = 0; i < 4; ++i) {
    if (i) output << ",";
    output << exact_point_json(result.corners[i]);
  }
  output << "],\"corner_contact_ids\":[";
  for (std::size_t i = 0; i < 4; ++i) {
    if (i) output << ",";
    output << ids_json(result.corner_contact_ids[i]);
  }
  output << "]";

  if (include_log) {
    output << ",\"edge_log\":[";
    for (std::size_t i = 0; i < result.edge_log.size(); ++i) {
      const EdgeEvidence& e = result.edge_log[i];
      if (i) output << ",";
      output << "{\"edge_index\":" << e.edge_index
             << ",\"edge_count\":" << e.edge_count
             << ",\"edge_start\":" << point_json(e.edge_start)
             << ",\"edge_end\":" << point_json(e.edge_end)
             << ",\"direction\":[" << to_string(e.direction_x) << ","
             << to_string(e.direction_y) << "]"
             << ",\"tangent_projection\":[" << to_string(e.min_tangent_dot)
             << "," << to_string(e.max_tangent_dot) << "]"
             << ",\"normal_projection\":[" << to_string(e.min_axis_dot) << ","
             << to_string(e.max_axis_dot) << "]"
             << ",\"area\":\"" << to_string(e.area_num) << "/"
             << to_string(e.area_den) << "\""
             << ",\"side_contact_ids\":" << ids_json(e.side_contact_ids)
             << ",\"opposite_contact_ids\":"
             << ids_json(e.opposite_contact_ids)
             << ",\"begin_contact_ids\":" << ids_json(e.begin_contact_ids)
             << ",\"end_contact_ids\":" << ids_json(e.end_contact_ids)
             << ",\"selected\":" << (e.selected ? "true" : "false")
             << ",\"decision\":\"" << json_escape(e.decision) << "\"}";
    }
    output << "]";
  }
  output << "}\n";
}

std::int64_t parse_int64(const std::string& token, const std::string& field) {
  if (token.empty()) {
    throw MerError(ErrorCategory::InputError, "MISSING_INTEGER",
                   field + " is empty");
  }
  errno = 0;
  char* end = nullptr;
  long long value = std::strtoll(token.c_str(), &end, 10);
  if (errno == ERANGE || end == token.c_str() || *end != '\0' ||
      value < std::numeric_limits<std::int64_t>::min() ||
      value > std::numeric_limits<std::int64_t>::max()) {
    throw MerError(ErrorCategory::InputError, "INTEGER_OUT_OF_RANGE",
                   field + " is not a signed 64-bit integer");
  }
  return static_cast<std::int64_t>(value);
}

std::size_t parse_size(const std::string& token) {
  std::uint64_t value = 0;
  auto [ptr, ec] =
      std::from_chars(token.data(), token.data() + token.size(), value);
  if (ec != std::errc{} || ptr != token.data() + token.size()) {
    throw MerError(ErrorCategory::InputError, "INVALID_LIMIT",
                   "limit must be a non-negative integer");
  }
  return static_cast<std::size_t>(value);
}

struct ActiveCase {
  std::int64_t run_number = 0;
  std::string id;
  std::size_t begin_line = 0;
  std::size_t point_limit = 0;
  bool limit_set = false;
  std::vector<Point> points;
  bool failed = false;
  std::size_t error_line = 0;
  ErrorCategory error_category = ErrorCategory::ComputationFailed;
  std::string error_code;
  std::string error_message;
};

void mark_failure(ActiveCase& active, const MerError& error, std::size_t line) {
  if (!active.failed) {
    active.failed = true;
    active.error_line = line;
    active.error_category = error.category();
    active.error_code = error.code();
    active.error_message = error.what();
  }
}

}  // namespace

BatchSummary run_batch(std::istream& input,
                       std::ostream& output,
                       const BatchOptions& options) {
  BatchSummary summary;
  std::string line;
  std::size_t line_number = 0;
  std::optional<ActiveCase> active;

  auto add_failure = [&](std::int64_t run_number, std::string case_id,
                         const MerError& error) {
    BatchFailure failure{run_number,
                         std::move(case_id),
                         line_number,
                         error.category(),
                         error.code(),
                         error.what()};
    write_failure(output, failure);
    summary.failures.push_back(std::move(failure));
    ++summary.failure_count;
  };

  while (std::getline(input, line)) {
    ++line_number;
    std::istringstream line_stream(line);
    std::string command;
    line_stream >> command;
    if (!line_stream || command.empty() || command.starts_with("#")) continue;

    try {
      if (command == "BEGIN") {
        if (active) {
          MerError error{ErrorCategory::StateConflict, "NESTED_CASE",
                         "BEGIN encountered before END of active case"};
          mark_failure(*active, error, line_number);
          continue;
        }
        if (summary.case_count >=
            options.max_cases) {
          throw MerError(ErrorCategory::ResourceExhausted, "CASE_LIMIT",
                         "configured maximum number of cases reached");
        }
        std::string id;
        line_stream >> id;
        if (!line_stream || id.empty()) {
          throw MerError(ErrorCategory::InputError, "MISSING_CASE_ID",
                         "BEGIN requires a case id");
        }
        ActiveCase next;
        next.run_number = static_cast<std::int64_t>(summary.case_count);
        next.id = id;
        next.begin_line = line_number;
        next.point_limit = options.max_points_per_case;
        active = std::move(next);
      } else if (command == "LIMIT") {
        if (!active) {
          throw MerError(ErrorCategory::StateConflict, "LIMIT_WITHOUT_CASE",
                         "LIMIT must follow BEGIN");
        }
        if (!active->points.empty()) {
          throw MerError(ErrorCategory::StateConflict, "LIMIT_AFTER_POINTS",
                         "case limit must be declared before points");
        }
        if (active->limit_set) {
          throw MerError(ErrorCategory::StateConflict, "DUPLICATE_LIMIT",
                         "a case may declare LIMIT once");
        }
        std::string token;
        line_stream >> token;
        std::size_t requested = parse_size(token);
        if (requested > options.max_points_per_case) {
          throw MerError(ErrorCategory::ResourceExhausted,
                         "POINT_LIMIT_TOO_LARGE",
                         "requested per-case limit exceeds runner limit");
        }
        active->point_limit = requested;
        active->limit_set = true;
      } else if (command == "POINT") {
        if (!active) {
          throw MerError(ErrorCategory::StateConflict, "POINT_WITHOUT_CASE",
                         "POINT must appear between BEGIN and END");
        }
        if (active->points.size() >= active->point_limit) {
          throw MerError(ErrorCategory::ResourceExhausted, "POINT_LIMIT",
                         "configured maximum number of points reached");
        }
        std::string xs;
        std::string ys;
        line_stream >> xs >> ys;
        Coord x = static_cast<Coord>(parse_int64(xs, "x"));
        Coord y = static_cast<Coord>(parse_int64(ys, "y"));
        Point p{x, y, static_cast<std::int64_t>(active->points.size())};
        active->points.push_back(p);
      } else if (command == "END") {
        if (!active) {
          throw MerError(ErrorCategory::StateConflict, "END_WITHOUT_BEGIN",
                         "END has no active case");
        }
        std::string extra;
        line_stream >> extra;
        if (!extra.empty()) {
          throw MerError(ErrorCategory::InputError, "UNEXPECTED_END_TOKEN",
                         "END takes no arguments");
        }
        ActiveCase current = std::move(*active);
        active.reset();
        if (current.failed) {
          BatchFailure failure{
              current.run_number,  current.id,
              current.error_line, current.error_category,
              current.error_code, current.error_message,
          };
          write_failure(output, failure);
          summary.failures.push_back(std::move(failure));
          ++summary.case_count;
          ++summary.failure_count;
          continue;
        }
        if (current.points.empty()) {
          throw MerError(ErrorCategory::InputError, "EMPTY_CASE",
                         "case contains no POINT records");
        }

        try {
          AlgorithmOptions algorithm_options;
          algorithm_options.retain_edge_log = options.retain_edge_log;
          MinimumRectangle result =
              minimum_area_rectangle(current.points, algorithm_options);
          write_success(output, current.run_number, current.id,
                        current.begin_line, current.points, result,
                        options.retain_edge_log);
          ++summary.case_count;
          ++summary.success_count;
        } catch (const MerError& error) {
          BatchFailure failure{current.run_number, current.id, line_number,
                               error.category(), error.code(), error.what()};
          write_failure(output, failure);
          summary.failures.push_back(std::move(failure));
          ++summary.case_count;
          ++summary.failure_count;
        } catch (const std::bad_alloc&) {
          MerError error{ErrorCategory::ResourceExhausted,
                         "COMPUTATION_ALLOCATION_FAILED",
                         "not enough memory to compute rectangle"};
          BatchFailure failure{current.run_number, current.id, line_number,
                               error.category(), error.code(), error.what()};
          write_failure(output, failure);
          summary.failures.push_back(std::move(failure));
          ++summary.case_count;
          ++summary.failure_count;
        } catch (const std::exception& error) {
          MerError wrapped{ErrorCategory::ComputationFailed, "UNEXPECTED",
                           error.what()};
          BatchFailure failure{current.run_number, current.id, line_number,
                               wrapped.category(), wrapped.code(),
                               wrapped.what()};
          write_failure(output, failure);
          summary.failures.push_back(std::move(failure));
          ++summary.case_count;
          ++summary.failure_count;
        }
      } else {
        throw MerError(ErrorCategory::InputError, "UNKNOWN_COMMAND",
                       "unknown command: " + command);
      }
    } catch (const MerError& error) {
      if (active) {
        mark_failure(*active, error, line_number);
      } else {
        add_failure(-1, "", error);
      }
    } catch (const std::bad_alloc&) {
      MerError error{ErrorCategory::ResourceExhausted, "ALLOCATION_FAILED",
                     "not enough memory while parsing batch input"};
      if (active) {
        mark_failure(*active, error, line_number);
      } else {
        add_failure(-1, "", error);
      }
    }
  }

  if (active) {
    if (!active->failed) {
      active->failed = true;
      active->error_line = active->begin_line;
      active->error_category = ErrorCategory::StateConflict;
      active->error_code = "UNTERMINATED_CASE";
      active->error_message = "end of stream reached before END";
    }
    BatchFailure failure{active->run_number,
                         active->id,
                         active->error_line,
                         active->error_category,
                         active->error_code,
                         active->error_message};
    write_failure(output, failure);
    summary.failures.push_back(std::move(failure));
    ++summary.case_count;
    ++summary.failure_count;
  }

  return summary;
}

}  // namespace mer
