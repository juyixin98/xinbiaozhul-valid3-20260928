#include <filesystem>
#include <fstream>
#include <iostream>
#include <random>
#include <string>
#include <vector>

#include "geometry/types.hpp"

// Deterministic synthetic fixture generator. All data is local and synthetic;
// fixed seeds make every file reproducible.
namespace {

using mileage::Vec2;
using mileage::cpp_int;

void write_polyline(const std::filesystem::path& path,
                    const std::vector<Vec2>& pts, const std::string& header) {
  std::ofstream out(path);
  out << "# " << header << "\n";
  for (const auto& p : pts) out << p.x << " " << p.y << "\n";
}

std::vector<Vec2> random_walk(size_t n, uint64_t seed, cpp_int base,
                              int64_t step_max) {
  std::mt19937_64 rng(seed);
  std::uniform_int_distribution<int64_t> step(-step_max, step_max);
  std::vector<Vec2> pts;
  pts.reserve(n);
  cpp_int x = base, y = base;
  pts.push_back({x, y});
  for (size_t i = 1; i < n; ++i) {
    x += step(rng);
    y += step(rng);
    pts.push_back({x, y});
  }
  return pts;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc < 2) {
    std::cerr << "usage: gen_fixtures <output-dir>\n";
    return 2;
  }
  const std::filesystem::path dir = argv[1];
  std::filesystem::create_directories(dir);

  // Hand-checkable small polyline.
  write_polyline(dir / "simple.poly",
                 {{0, 0}, {10, 0}, {10, 10}},
                 "simple L-shaped polyline, hand-verifiable");

  // Self-intersecting: segment 3 crosses segment 0.
  write_polyline(dir / "self_intersect.poly",
                 {{0, 0}, {10, 0}, {10, 10}, {0, 10}, {5, -1}},
                 "figure with a proper crossing between seg 0 and seg 3");

  // Zero-length segments and a duplicate-vertex run.
  write_polyline(dir / "zero_length.poly",
                 {{0, 0}, {5, 0}, {5, 0}, {5, 0}, {5, 8}},
                 "zero-length segments keep chainage semantics");

  // Huge coordinates near 10^18 (beyond exact double range).
  const cpp_int big("1000000000000000000");
  write_polyline(dir / "huge_coords.poly",
                 {{big, big},
                  {big + 1000000, big},
                  {big + 1000000, big + 1000000},
                  {big + 2000000, big + 500000}},
                 "coordinates near 1e18, exact predicates required");

  // Large random-walk polyline for scale checks.
  write_polyline(dir / "large_random.poly", random_walk(50001, 42, 0, 1000),
                 "seeded random walk, 50k segments, seed=42");

  // Batch queries against large_random.poly, plus edge cases.
  {
    std::ofstream q(dir / "queries.txt");
    q << "# Q <id> <x> <y> [label]\n";
    std::mt19937_64 rng(7);
    std::uniform_int_distribution<int64_t> coord(-60000, 60000);
    for (int i = 0; i < 200; ++i) {
      q << "Q q" << i << " " << coord(rng) << " " << coord(rng)
        << " synthetic-tag-" << i << "\n";
    }
    q << "Q origin 0 0 first-vertex\n";
    q << "Q beyond-limit " << (cpp_int(1) << 130) << " 0 overflow-check\n";
  }

  std::cout << "fixtures written to " << dir << "\n";
  return 0;
}
