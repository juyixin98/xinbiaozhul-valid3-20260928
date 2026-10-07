// Test driver for the convex Minkowski / support-query backend.
//
// Design notes:
//   - Expected results come from tests/reference.hpp (independent
//     enumeration + convex hull), never from the core algorithm itself.
//   - Every check logs the test id, the computation step, and the judgment
//     basis; failures are categorized as assertion / unexpected-exception /
//     missing-exception.
//   - Each run carries a run id (argv: --run-id <id>) plus library and
//     algorithm version strings, and every input fixture is fingerprinted
//     so logs can be traced back to concrete inputs.

#include <boost/version.hpp>

#include <exception>
#include <functional>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "algo/minkowski.hpp"
#include "algo/support.hpp"
#include "geom/polygon.hpp"
#include "reference.hpp"
#include "version.hpp"

namespace {

struct TestFailure {};

struct Ctx {
  std::string id;
  void check(bool ok, const std::string& basis) {
    if (!ok) {
      std::cout << "  [" << id << "] FAIL category=assertion basis=" << basis
                << '\n';
      throw TestFailure{};
    }
    std::cout << "  [" << id << "] step-pass basis=" << basis << '\n';
  }
};

// FNV-1a fingerprint over the exact string form of a point list, so each
// logged test can be tied to the concrete input it ran on.
std::string fingerprint(const std::vector<geom::Point>& pts) {
  uint64_t h = 1469598103934665603ULL;
  for (const auto& p : pts) {
    for (char c : geom::to_string(p)) {
      h ^= static_cast<unsigned char>(c);
      h *= 1099511628211ULL;
    }
  }
  std::ostringstream os;
  os << std::hex << h;
  return os.str();
}

// Canonical cyclic form: rotate so the (y,x)-lowest vertex comes first.
std::vector<geom::Point> canonical(std::vector<geom::Point> v) {
  if (v.empty()) return v;
  size_t best = 0;
  for (size_t i = 1; i < v.size(); ++i)
    if (v[i].y < v[best].y || (v[i].y == v[best].y && v[i].x < v[best].x))
      best = i;
  std::rotate(v.begin(), v.begin() + best, v.end());
  return v;
}

bool same_cycle(const std::vector<geom::Point>& a,
                const std::vector<geom::Point>& b) {
  return canonical(a) == canonical(b);
}

geom::Point P(int x, int y) {
  return {geom::cpp_rational(x), geom::cpp_rational(y)};
}

// ---------------------------------------------------------------- fixtures

std::vector<geom::Point> square_cw() {
  return {P(0, 0), P(0, 2), P(2, 2), P(2, 0)};
}
std::vector<geom::Point> square_ccw() {
  return {P(0, 0), P(2, 0), P(2, 2), P(0, 2)};
}
std::vector<geom::Point> triangle() { return {P(0, 0), P(3, 0), P(1, 2)}; }

// Strictly convex 12-gon: parabola samples plus a closing bottom vertex.
std::vector<geom::Point> parabola_ngon() {
  std::vector<geom::Point> pts;
  for (int i = -5; i <= 5; ++i) pts.push_back(P(i, i * i));
  pts.push_back(P(0, -1));
  return ref::convex_hull(std::move(pts));
}

// ------------------------------------------------------------------- tests

void test_winding_unification(Ctx& c) {
  auto poly = geom::ConvexPolygon::from_points(square_cw());
  c.check(poly.kind() == geom::ShapeKind::Polygon && poly.size() == 4,
          "cw square normalizes to 4-vertex polygon");
  bool ccw = true;
  for (size_t i = 0; i < poly.size(); ++i)
    if (geom::cross(poly.edge(i), poly.edge((i + 1) % poly.size())) <= 0)
      ccw = false;
  c.check(ccw, "all consecutive edge turns are strictly CCW after normalize");
}

void test_collinear_merge(Ctx& c) {
  std::vector<geom::Point> with_mid = {P(0, 0), P(1, 0), P(2, 0), P(2, 2),
                                       P(1, 2), P(0, 2), P(0, 1)};
  auto poly = geom::ConvexPolygon::from_points(with_mid);
  c.check(poly.size() == 4, "edge midpoints are merged away, 4 vertices");
  c.check(same_cycle(poly.vertices(), square_ccw()),
          "merged vertex set equals the plain square");
}

void test_nonconvex_rejection(Ctx& c) {
  bool caught = false;
  try {
    (void)geom::ConvexPolygon::from_points(
        {P(0, 0), P(2, 0), P(1, 1), P(2, 2), P(0, 2)});
  } catch (const geom::NonConvexError&) {
    caught = true;
  }
  c.check(caught, "concave pentagon rejected with NonConvexError");
}

void test_degenerate_point(Ctx& c) {
  auto pt = geom::ConvexPolygon::from_points({P(3, 4)});
  c.check(pt.kind() == geom::ShapeKind::Point,
          "single vertex collapses to ShapeKind::Point");
  auto tri = geom::ConvexPolygon::from_points(triangle());
  auto sum = geom::minkowski_sum(pt, tri);
  auto expect = ref::minkowski_enumerate({P(3, 4)}, triangle());
  c.check(same_cycle(sum.vertices(), expect),
          "point + triangle equals translated triangle (reference)");
  c.check(geom::support_value(pt, P(1, 1)) == geom::cpp_rational(7),
          "support of a point is the point itself dotted with d");
}

void test_degenerate_segment(Ctx& c) {
  auto seg = geom::ConvexPolygon::from_points({P(0, 0), P(2, 0), P(1, 0)});
  c.check(seg.kind() == geom::ShapeKind::Segment && seg.size() == 2,
          "collinear triple collapses to a 2-vertex segment");
  auto tri = geom::ConvexPolygon::from_points(triangle());
  auto sum = geom::minkowski_sum(seg, tri);
  auto expect = ref::minkowski_enumerate(seg.vertices(), triangle());
  c.check(same_cycle(sum.vertices(), expect),
          "segment + triangle matches enumerate+hull reference");
}

void test_sum_square_triangle(Ctx& c) {
  auto sq = geom::ConvexPolygon::from_points(square_ccw());
  auto tri = geom::ConvexPolygon::from_points(triangle());
  auto sum = geom::minkowski_sum(sq, tri);
  auto expect = ref::minkowski_enumerate(square_ccw(), triangle());
  c.check(sum.kind() == geom::ShapeKind::Polygon,
          "square + triangle stays a polygon");
  c.check(same_cycle(sum.vertices(), expect),
          "edge-merge result equals enumerate+hull reference, vertex count " +
              std::to_string(expect.size()));
}

void test_support_additivity(Ctx& c) {
  auto sq = geom::ConvexPolygon::from_points(square_ccw());
  auto tri = geom::ConvexPolygon::from_points(triangle());
  auto sum = geom::minkowski_sum(sq, tri);
  const geom::Point dirs[] = {P(1, 0), P(0, 1), P(1, 1), P(-2, 3), P(3, -1)};
  for (const auto& d : dirs) {
    const auto lhs = geom::support_value(sum, d);
    const auto rhs =
        geom::support_value(sq, d) + geom::support_value(tri, d);
    c.check(lhs == rhs, "h_{A+B}(d) == h_A(d) + h_B(d) for d=" +
                            geom::to_string(d));
  }
}

void test_support_vs_brute_reference(Ctx& c) {
  auto ngon = geom::ConvexPolygon::from_points(parabola_ngon());
  c.check(ngon.size() == 12, "parabola fixture is a strict 12-gon");
  const geom::Point dirs[] = {P(1, 0),  P(0, 1),  P(1, 1),   P(-1, 1),
                              P(-1, 0), P(0, -1), P(-3, -2), P(2, -5)};
  for (const auto& d : dirs) {
    const auto fast = geom::support_value(ngon, d);
    const auto slow = ref::support_brute(ngon.vertices(), d);
    c.check(fast == slow, "binary-search support == brute force for d=" +
                              geom::to_string(d));
  }
}

void test_translation_property(Ctx& c) {
  auto tri = geom::ConvexPolygon::from_points(triangle());
  auto shifted = geom::ConvexPolygon::from_points(
      {P(5, -2), P(8, -2), P(6, 0)});  // triangle + (5,-2)
  const geom::Point d = P(2, 3);
  const auto expect =
      geom::support_value(tri, d) + geom::dot(P(5, -2), d);
  c.check(geom::support_value(shifted, d) == expect,
          "h_{A+t}(d) == h_A(d) + <t,d>");
}

void test_segment_segment(Ctx& c) {
  auto s1 = geom::ConvexPolygon::from_points({P(0, 0), P(2, 0)});
  auto s2 = geom::ConvexPolygon::from_points({P(0, 0), P(1, 0)});
  auto par = geom::minkowski_sum(s1, s2);
  c.check(par.kind() == geom::ShapeKind::Segment,
          "parallel segments sum to a segment");
  auto s3 = geom::ConvexPolygon::from_points({P(0, 0), P(0, 2)});
  auto perp = geom::minkowski_sum(s1, s3);
  c.check(perp.kind() == geom::ShapeKind::Polygon && perp.size() == 4,
          "perpendicular segments sum to a 4-vertex rectangle");
  c.check(same_cycle(perp.vertices(), square_ccw()),
          "the rectangle is exactly the 2x2 square");
}

// ------------------------------------------------------------------ driver

struct TestCase {
  const char* id;
  std::vector<geom::Point> (*input)();
  void (*fn)(Ctx&);
};

std::string run_id;
int g_passed = 0;
int g_failed = 0;

void run(const TestCase& tc) {
  const auto input = tc.input();
  std::cout << "[TEST " << tc.id << "] run=" << run_id
            << " input_vertices=" << input.size()
            << " input_hash=" << fingerprint(input) << '\n';
  Ctx ctx{tc.id};
  try {
    tc.fn(ctx);
    ++g_passed;
    std::cout << "[TEST " << tc.id << "] PASS\n";
  } catch (const TestFailure&) {
    ++g_failed;
  } catch (const std::exception& e) {
    ++g_failed;
    std::cout << "  [" << tc.id
              << "] FAIL category=unexpected-exception what=" << e.what()
              << '\n';
    std::cout << "[TEST " << tc.id << "] FAIL\n";
  }
}

}  // namespace

int main(int argc, char** argv) {
  run_id = "manual";
  for (int i = 1; i + 1 < argc; ++i)
    if (std::string(argv[i]) == "--run-id") run_id = argv[i + 1];

  std::cout << "[RUN " << run_id << "] geom_version=" << geom::kVersion
            << " algorithm=" << geom::kAlgorithmId
            << " boost=" << BOOST_LIB_VERSION << '\n';

  const TestCase tests[] = {
      {"winding_unification", square_cw, test_winding_unification},
      {"collinear_merge", square_ccw, test_collinear_merge},
      {"nonconvex_rejection", triangle, test_nonconvex_rejection},
      {"degenerate_point", triangle, test_degenerate_point},
      {"degenerate_segment", triangle, test_degenerate_segment},
      {"sum_square_triangle", square_ccw, test_sum_square_triangle},
      {"support_additivity", square_ccw, test_support_additivity},
      {"support_vs_brute_reference", parabola_ngon,
       test_support_vs_brute_reference},
      {"translation_property", triangle, test_translation_property},
      {"segment_segment", square_ccw, test_segment_segment},
  };
  for (const auto& tc : tests) run(tc);

  std::cout << "[SUMMARY] run=" << run_id << " passed=" << g_passed
            << " failed=" << g_failed << '\n';
  return g_failed == 0 ? 0 : 1;
}
