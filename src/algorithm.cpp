#include "mer/algorithm.hpp"

#include <algorithm>
#include <array>
#include <set>

#include "mer/errors.hpp"

namespace mer {

namespace {

using IdSet = std::set<std::int64_t>;

std::vector<std::int64_t> ids_sorted(const IdSet& ids) {
  return {ids.begin(), ids.end()};
}

Decimal decimal_epsilon() {
  static const Decimal eps = Decimal{"1e-90"};
  return eps;
}

Fraction linear_coord(Coord origin,
                      Integer tangent,
                      Integer normal,
                      Integer dx,
                      Integer dy,
                      Integer den,
                      bool x_component) {
  Integer dir_part = x_component ? dx : dy;
  Integer norm_part = x_component ? -dy : dx;
  Integer n = Integer{origin} * den + tangent * dir_part + normal * norm_part;
  return Fraction{n, den};
}

std::array<ExactPoint, 4> make_corners(const Point& a,
                                       Integer dx,
                                       Integer dy,
                                       Integer min_t,
                                       Integer max_t,
                                       Integer min_s,
                                       Integer max_s) {
  Integer den = dx * dx + dy * dy;
  if (den == 0) {
    throw MerError(ErrorCategory::ComputationFailed, "ZERO_EDGE_LENGTH",
                   "internal hull edge has zero length");
  }

  std::array<ExactPoint, 4> result;
  result[0].x = linear_coord(a.x, min_t, min_s, dx, dy, den, true);
  result[0].y = linear_coord(a.y, min_t, min_s, dx, dy, den, false);
  result[1].x = linear_coord(a.x, max_t, min_s, dx, dy, den, true);
  result[1].y = linear_coord(a.y, max_t, min_s, dx, dy, den, false);
  result[2].x = linear_coord(a.x, max_t, max_s, dx, dy, den, true);
  result[2].y = linear_coord(a.y, max_t, max_s, dx, dy, den, false);
  result[3].x = linear_coord(a.x, min_t, max_s, dx, dy, den, true);
  result[3].y = linear_coord(a.y, min_t, max_s, dx, dy, den, false);
  return result;
}

void add_corner_contacts(MinimumRectangle& result,
                         const IdSet& side,
                         const IdSet& opposite,
                         const IdSet& begin,
                         const IdSet& end) {
  IdSet corners[4];
  std::set_intersection(side.begin(), side.end(), begin.begin(), begin.end(),
                        std::inserter(corners[0], corners[0].end()));
  std::set_intersection(side.begin(), side.end(), end.begin(), end.end(),
                        std::inserter(corners[1], corners[1].end()));
  std::set_intersection(opposite.begin(), opposite.end(), end.begin(),
                        end.end(),
                        std::inserter(corners[2], corners[2].end()));
  std::set_intersection(opposite.begin(), opposite.end(), begin.begin(),
                        begin.end(),
                        std::inserter(corners[3], corners[3].end()));
  for (std::size_t i = 0; i < 4; ++i) {
    result.corner_contact_ids[i] = ids_sorted(corners[i]);
  }
}

void fill_error_bounds(MinimumRectangle& result) {
  const Decimal eps = decimal_epsilon();
  result.area_decimal = result.area.value();
  result.area_error_bound =
      eps * (Decimal{1} + decimal_from_integer(boost::multiprecision::abs(
                              result.area.num)));
  if (result.area_error_bound < eps) {
    result.area_error_bound = eps;
  }

  for (std::size_t i = 0; i < 4; ++i) {
    Decimal xv = result.corners[i].x.value();
    Decimal yv = result.corners[i].y.value();
    result.corner_error_x[i] = eps * (Decimal{1} + abs(xv));
    result.corner_error_y[i] = eps * (Decimal{1} + abs(yv));
  }
}

MinimumRectangle empty_result() {
  MinimumRectangle result;
  result.hull_kind = HullKind::Empty;
  result.area = integer_fraction(Integer{0});
  fill_error_bounds(result);
  return result;
}

MinimumRectangle point_result(const std::vector<Point>& points) {
  MinimumRectangle result;
  result.hull_kind = HullKind::Point;
  Point p = points.front();
  Fraction x = integer_fraction(integer_from_coord(p.x));
  Fraction y = integer_fraction(integer_from_coord(p.y));
  for (auto& c : result.corners) {
    c = {x, y};
  }
  IdSet all;
  for (const Point& q : points) {
    all.insert(q.id);
  }
  auto ids = ids_sorted(all);
  for (auto& bucket : result.corner_contact_ids) {
    bucket = ids;
  }
  result.area = integer_fraction(Integer{0});
  fill_error_bounds(result);
  return result;
}

MinimumRectangle segment_result(const std::vector<Point>& points,
                                 const Hull& hull) {
  MinimumRectangle result;
  result.hull_kind = HullKind::Segment;
  const Point a = hull.vertices[0];
  const Point b = hull.vertices[1];
  Integer dx = integer_from_coord(b.x) - integer_from_coord(a.x);
  Integer dy = integer_from_coord(b.y) - integer_from_coord(a.y);
  Integer den = dx * dx + dy * dy;
  if (den == 0) {
    throw MerError(ErrorCategory::ComputationFailed, "ZERO_SEGMENT",
                   "segment hull collapsed to a point");
  }

  IdSet min_t_ids;
  IdSet max_t_ids;
  bool first = true;
  Integer min_t{0};
  Integer max_t{0};
  for (const Point& p : points) {
    Integer t = (integer_from_coord(p.x) - integer_from_coord(a.x)) * dx +
                (integer_from_coord(p.y) - integer_from_coord(a.y)) * dy;
    if (first || t < min_t) {
      min_t = t;
      min_t_ids.clear();
    }
    if (first || t > max_t) {
      max_t = t;
      max_t_ids.clear();
    }
    if (t == min_t) min_t_ids.insert(p.id);
    if (t == max_t) max_t_ids.insert(p.id);
    first = false;
  }

  result.corners =
      make_corners(a, dx, dy, min_t, max_t, Integer{0}, Integer{0});
  result.corner_contact_ids[0] = ids_sorted(min_t_ids);
  result.corner_contact_ids[1] = ids_sorted(max_t_ids);
  result.corner_contact_ids[2] = ids_sorted(max_t_ids);
  result.corner_contact_ids[3] = ids_sorted(min_t_ids);
  result.area = integer_fraction(Integer{0});
  fill_error_bounds(result);

  EdgeEvidence evidence;
  evidence.edge_count = 1;
  evidence.edge_start = a;
  evidence.edge_end = b;
  evidence.direction_x = dx;
  evidence.direction_y = dy;
  evidence.min_tangent_dot = min_t;
  evidence.max_tangent_dot = max_t;
  evidence.min_axis_dot = 0;
  evidence.max_axis_dot = 0;
  evidence.area_num = 0;
  evidence.area_den = den;
  IdSet all;
  for (const Point& p : points) all.insert(p.id);
  evidence.side_contact_ids = evidence.opposite_contact_ids = ids_sorted(all);
  evidence.begin_contact_ids = ids_sorted(min_t_ids);
  evidence.end_contact_ids = ids_sorted(max_t_ids);
  evidence.selected = true;
  evidence.decision = "segment: area is zero without a zero denominator";
  result.edge_log.push_back(std::move(evidence));
  return result;
}

}  // namespace

MinimumRectangle minimum_area_rectangle(const std::vector<Point>& points,
                                       const AlgorithmOptions& options) {
  if (points.empty()) return empty_result();

  for (const Point& p : points) {
    if (p.id < 0) {
      throw MerError(ErrorCategory::InputError, "NEGATIVE_POINT_ID",
                     "point identifiers must be non-negative");
    }
  }

  Hull hull;
  try {
    hull = convex_hull(points);
  } catch (const std::bad_alloc&) {
    throw MerError(ErrorCategory::ResourceExhausted, "HULL_ALLOCATION_FAILED",
                   "not enough memory to build convex hull");
  }

  if (hull.kind == HullKind::Point) return point_result(points);
  if (hull.kind == HullKind::Segment) return segment_result(points, hull);

  MinimumRectangle result;
  result.hull_kind = HullKind::Polygon;
  bool has_best = false;
  Integer best_area_num{0};
  Integer best_area_den{1};

  const std::size_t n = hull.vertices.size();
  for (std::size_t i = 0; i < n; ++i) {
    const Point a = hull.vertices[i];
    const Point b = hull.vertices[(i + 1) % n];
    Integer dx = integer_from_coord(b.x) - integer_from_coord(a.x);
    Integer dy = integer_from_coord(b.y) - integer_from_coord(a.y);
    Integer len2 = dx * dx + dy * dy;
    if (len2 == 0) {
      throw MerError(ErrorCategory::ComputationFailed, "ZERO_EDGE_LENGTH",
                     "internal hull edge has zero length");
    }

    IdSet side;
    IdSet opposite;
    IdSet begin;
    IdSet end;
    bool first = true;
    Integer min_t{0}, max_t{0}, min_s{0}, max_s{0};

    for (const Point& p : points) {
      Integer px = integer_from_coord(p.x) - integer_from_coord(a.x);
      Integer py = integer_from_coord(p.y) - integer_from_coord(a.y);
      Integer t = px * dx + py * dy;
      Integer s = -px * dy + py * dx;
      if (first) {
        min_t = max_t = t;
        min_s = max_s = s;
      } else {
        min_t = std::min(min_t, t);
        max_t = std::max(max_t, t);
        min_s = std::min(min_s, s);
        max_s = std::max(max_s, s);
      }
      first = false;
    }

    // Second pass is needed because extrema were unknown during the first one.
    for (const Point& p : points) {
      Integer px = integer_from_coord(p.x) - integer_from_coord(a.x);
      Integer py = integer_from_coord(p.y) - integer_from_coord(a.y);
      Integer t = px * dx + py * dy;
      Integer s = -px * dy + py * dx;
      if (s == min_s) side.insert(p.id);
      if (s == max_s) opposite.insert(p.id);
      if (t == min_t) begin.insert(p.id);
      if (t == max_t) end.insert(p.id);
    }

    Integer width_t = max_t - min_t;
    Integer width_s = max_s - min_s;
    Integer area_num = width_t * width_s;

    bool choose = false;
    std::string decision;
    if (!has_best) {
      choose = true;
      decision = "first exhaustive hull-edge candidate";
    } else {
      Integer lhs = area_num * best_area_den;
      Integer rhs = best_area_num * len2;
      if (lhs < rhs) {
        choose = true;
        decision = "smaller exact rational area";
      } else if (lhs == rhs) {
        decision = "equal area; retained lower edge index";
      } else {
        decision = "larger exact rational area";
      }
    }

    EdgeEvidence evidence;
    evidence.edge_index = i;
    evidence.edge_count = n;
    evidence.edge_start = a;
    evidence.edge_end = b;
    evidence.direction_x = dx;
    evidence.direction_y = dy;
    evidence.min_tangent_dot = min_t;
    evidence.max_tangent_dot = max_t;
    evidence.min_axis_dot = min_s;
    evidence.max_axis_dot = max_s;
    evidence.area_num = area_num;
    evidence.area_den = len2;
    evidence.side_contact_ids = ids_sorted(side);
    evidence.opposite_contact_ids = ids_sorted(opposite);
    evidence.begin_contact_ids = ids_sorted(begin);
    evidence.end_contact_ids = ids_sorted(end);

    if (choose) {
      has_best = true;
      best_area_num = area_num;
      best_area_den = len2;
      evidence.selected = true;
      result.selected_edge_index = i;
      result.corners = make_corners(a, dx, dy, min_t, max_t, min_s, max_s);
      add_corner_contacts(result, side, opposite, begin, end);
      result.area = Fraction{area_num, len2};
    }
    evidence.decision = decision;
    if (options.retain_edge_log) {
      result.edge_log.push_back(std::move(evidence));
    }
  }

  if (!has_best) {
    throw MerError(ErrorCategory::ComputationFailed, "NO_CANDIDATE",
                   "no rectangle candidate was produced");
  }
  fill_error_bounds(result);
  return result;
}

}  // namespace mer
