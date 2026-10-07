#include <mer/hull.hpp>
#include <mer/types.hpp>

#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <string>

int main(int argc, char** argv) {
  if (argc != 2) {
    std::cerr << "Usage: mer_generate_fixture square|diamond|triangle|degenerate\n";
    return 2;
  }
  std::string shape = argv[1];

  std::vector<mer::Point> points;
  if (shape == "square") {
    points = {{0, 0, 0}, {1, 0, 1}, {1, 1, 2}, {0, 1, 3}};
  } else if (shape == "diamond") {
    points = {{0, -1, 0}, {1, 0, 1}, {0, 1, 2}, {-1, 0, 3}};
  } else if (shape == "triangle") {
    points = {{0, 0, 0}, {4, 0, 1}, {0, 3, 2}};
  } else if (shape == "degenerate") {
    points = {{2, 2, 0},
              {2, 2, 1},
              {-1, -1, 2},
              {0, 0, 3},
              {5, 5, 4},
              {5, 5, 5}};
  } else {
    std::cerr << "unknown fixture: " << shape << "\n";
    return 2;
  }

  std::cout << "BEGIN " << shape << "\n";
  for (const auto& p : points) {
    std::cout << "POINT " << p.x << " " << p.y << "\n";
  }
  std::cout << "END\n";
  return 0;
}
