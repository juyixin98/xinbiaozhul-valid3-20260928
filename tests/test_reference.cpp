#include "mer/algorithm.hpp"
#include "mer/batch.hpp"
#include "mer/errors.hpp"
#include "mer/predicates.hpp"

#include <cstdlib>
#include <iostream>
#include <map>
#include <sstream>
#include <string>
#include <vector>

namespace {

using mer::Decimal;
using mer::Fraction;
using mer::Integer;
using mer::MinimumRectangle;
using mer::Point;

int failures = 0;

void check(bool condition, const std::string& message) {
  if (!condition) {
    std::cerr << "FAIL: " << message << "\n";
    ++failures;
  }
}

Integer integer(long long value) { return Integer{value}; }

struct Candidate {
  Point start;
  Integer dx;
  Integer dy;
  Integer min_t;
  Integer max_t;
  Integer min_s;
  Integer max_s;
  Fraction area{Integer{0}, Integer{1}};
};

bool contains_point(const Candidate& c, const Point& p) {
  Integer px = integer(p.x) - integer(c.start.x);
  Integer py = integer(p.y) - integer(c.start.y);
  Integer t = px * c.dx + py * c.dy;
  Integer s = -px * c.dy + py * c.dx;
  return c.min_t <= t && t <= c.max_t && c.min_s <= s && s <= c.max_s;
}

bool better(const Candidate& a, const Candidate& b) {
  Integer lhs = a.area.num * b.area.den;
  Integer rhs = b.area.num * a.area.den;
  if (lhs != rhs) return lhs < rhs;
  if (a.dx != b.dx) return a.dx < b.dx;
  if (a.dy != b.dy) return a.dy < b.dy;
  if (a.start.x != b.start.x) return a.start.x < b.start.x;
  return a.start.y < b.start.y;
}

// Independent reference: enumerate every point-pair direction rather than
// deriving hull edges from the implementation under test.
Candidate reference_best(const std::vector<Point>& points) {
  bool has = false;
  Candidate best;

  for (const Point& a : points) {
    for (const Point& b : points) {
      Integer dx = integer(b.x) - integer(a.x);
      Integer dy = integer(b.y) - integer(a.y);
      if (dx == 0 && dy == 0) continue;

      Candidate c;
      c.start = a;
      c.dx = dx;
      c.dy = dy;
      bool first = true;
      for (const Point& p : points) {
        Integer px = integer(p.x) - integer(a.x);
        Integer py = integer(p.y) - integer(a.y);
        Integer t = px * dx + py * dy;
        Integer s = -px * dy + py * dx;
        if (first || t < c.min_t) c.min_t = t;
        if (first || t > c.max_t) c.max_t = t;
        if (first || s < c.min_s) c.min_s = s;
        if (first || s > c.max_s) c.max_s = s;
        first = false;
      }
      Integer den = dx * dx + dy * dy;
      c.area = Fraction{(c.max_t - c.min_t) * (c.max_s - c.min_s), den};

      for (const Point& p : points) {
        check(contains_point(c, p),
              "reference candidate must contain every input point");
      }
      if (!has || better(c, best)) {
        best = c;
        has = true;
      }
    }
  }
  return best;
}

Candidate selected_candidate(const MinimumRectangle& result) {
  const auto& e = result.edge_log.at(result.selected_edge_index);
  Candidate c;
  c.start = e.edge_start;
  c.dx = e.direction_x;
  c.dy = e.direction_y;
  c.min_t = e.min_tangent_dot;
  c.max_t = e.max_tangent_dot;
  c.min_s = e.min_axis_dot;
  c.max_s = e.max_axis_dot;
  c.area = result.area;
  return c;
}

void run_fixture(const std::string& name,
                 const std::vector<Point>& points,
                 const std::string& expected_area) {
  MinimumRectangle result = mer::minimum_area_rectangle(points);
  check(mer::to_string(result.area) == expected_area,
        name + ": expected exact area " + expected_area + ", got " +
            mer::to_string(result.area));

  Candidate ref = reference_best(points);
  Candidate selected = selected_candidate(result);
  check(selected.area == ref.area,
        name + ": selected area agrees with independent reference");

  for (const Point& p : points) {
    check(contains_point(selected, p),
          name + ": selected rectangle contains point id " +
              std::to_string(p.id));
  }

  // Every hull-edge projection record is independently replayed here.
  for (const auto& e : result.edge_log) {
    for (const Point& p : points) {
      Integer px = integer(p.x) - integer(e.edge_start.x);
      Integer py = integer(p.y) - integer(e.edge_start.y);
      Integer t = px * e.direction_x + py * e.direction_y;
      Integer s = -px * e.direction_y + py * e.direction_x;
      check(e.min_tangent_dot <= t && t <= e.max_tangent_dot,
            name + ": edge " + std::to_string(e.edge_index) +
                " tangent projection out of range");
      check(e.min_axis_dot <= s && s <= e.max_axis_dot,
            name + ": edge " + std::to_string(e.edge_index) +
                " normal projection out of range");
    }
  }

  for (std::size_t i = 0; i < 4; ++i) {
    check(result.corner_error_x[i] > Decimal{0},
          name + ": positive x error bound");
    check(result.corner_error_y[i] > Decimal{0},
          name + ": positive y error bound");
  }
}

void test_degeneracies() {
  MinimumRectangle point =
      mer::minimum_area_rectangle({{3, -7, 0}, {3, -7, 1}});
  check(point.hull_kind == mer::HullKind::Point, "duplicates make point hull");
  check(mer::to_string(point.area) == "0/1", "point area is 0/1");
  check(point.corners[0].x == Fraction{integer(3), integer(1)},
        "point corner x");
  check(point.corner_contact_ids[0] ==
            std::vector<std::int64_t>({0, 1}),
        "point keeps duplicate IDs as contacts");

  std::vector<Point> line{{-2, -2, 0}, {1, 1, 1}, {4, 4, 2}, {0, 0, 3}};
  MinimumRectangle segment = mer::minimum_area_rectangle(line);
  check(segment.hull_kind == mer::HullKind::Segment, "collinear is segment");
  check(mer::to_string(segment.area) == "0/1", "segment area is zero");
  check(segment.edge_log.at(0).area_den > 0,
        "segment uses nonzero direction denominator");
  Candidate selected = selected_candidate(segment);
  for (const Point& p : line) check(contains_point(selected, p), "segment contains point");
}

void test_tie_rule() {
  // Axis-aligned rectangle: horizontal and vertical directions tie.
  std::vector<Point> rectangle{{0, 0, 0}, {3, 0, 1}, {3, 1, 2}, {0, 1, 3}};
  MinimumRectangle result = mer::minimum_area_rectangle(rectangle);
  check(mer::to_string(result.area) == "3/1", "rectangle area 3");
  check(result.selected_edge_index == 0,
        "area tie resolved by lowest hull edge index");
  check(result.edge_log.size() == 4, "four hull edge directions logged");
  check(result.edge_log[0].decision.find("first") != std::string::npos,
        "first candidate decision recorded");
  check(result.edge_log[1].decision.find("equal area") != std::string::npos,
        "tie decision recorded");
}

void test_predicate_separation() {
  Point a{0, 0, 0};
  Point b{10000, 0, 1};
  Point c{1, 1, 2};
  auto report = mer::near_collinear(a, b, c, Decimal{"1.5"});
  check(report.near_collinear, "tiny signed area is near-collinear");
  check(report.exact_cross == integer(10000), "exact orientation is still nonzero");

  MinimumRectangle result =
      mer::minimum_area_rectangle({{0, 0, 0}, {10000, 0, 1}, {1, 1, 2}});
  check(result.hull_kind == mer::HullKind::Polygon,
        "near-collinearity does not collapse exact hull");

  bool threw = false;
  try {
    (void)mer::near_collinear(a, a, c, Decimal{"0.001"});
  } catch (const mer::MerError& error) {
    threw = true;
    check(error.category() == mer::ErrorCategory::StateConflict,
          "zero predicate segment category");
    check(std::string(error.code()) == "ZERO_REFERENCE_SEGMENT",
          "zero predicate segment code");
  }
  check(threw, "zero predicate segment is a distinguishable conflict");
}

void test_batch_errors() {
  std::istringstream input(R"(
BEGIN good
POINT 0 0
POINT 1 0
POINT 1 1
POINT 0 1
END
BEGIN bad_state
POINT 1 1
LIMIT 4
END
BEGIN bad_number
POINT 1 notanint
END
BEGIN unfinished
POINT 0 0
)");
  std::ostringstream output;
  mer::BatchOptions options;
  options.retain_edge_log = true;
  auto summary = mer::run_batch(input, output, options);
  std::string text = output.str();
  check(summary.success_count == 1, "one successful batch case");
  check(summary.failure_count == 3, "three failed batch cases");
  check(text.find("\"error_category\":\"STATE_CONFLICT\"") != std::string::npos,
        "batch reports state conflict");
  check(text.find("\"error_code\":\"LIMIT_AFTER_POINTS\"") !=
            std::string::npos,
        "batch reports concrete state code");
  check(text.find("\"error_category\":\"INPUT_ERROR\"") != std::string::npos,
        "batch reports input error");
  check(text.find("UNTERMINATED_CASE") != std::string::npos,
        "batch reports unterminated case");
  check(text.find("\"run_number\":") != std::string::npos,
        "batch output has replayable run numbers");
}

}  // namespace

int main() {
  run_fixture("square", {{0, 0, 0}, {1, 0, 1}, {1, 1, 2}, {0, 1, 3}},
              "1/1");
  run_fixture("diamond",
              {{0, -1, 0}, {1, 0, 1}, {0, 1, 2}, {-1, 0, 3}}, "2/1");
  run_fixture("triangle", {{0, 0, 0}, {4, 0, 1}, {0, 3, 2}},
              "12/1");
  run_fixture("interior",
              {{0, 0, 0}, {4, 0, 1}, {4, 3, 2}, {0, 3, 3}, {1, 1, 4},
               {3, 2, 5}},
              "12/1");
  test_degeneracies();
  test_tie_rule();
  test_predicate_separation();
  test_batch_errors();

  if (failures) {
    std::cerr << failures << " test check(s) failed\n";
    return EXIT_FAILURE;
  }
  std::cout << "all reference checks passed\n";
  return EXIT_SUCCESS;
}
