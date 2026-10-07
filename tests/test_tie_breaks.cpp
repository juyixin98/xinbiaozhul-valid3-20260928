#include "mbr/solver.hpp"
#include "test_support.hpp"

int main() {
    using namespace mbr;
    SolverOptions options;

    // For an axis-aligned square, all four directions tie. The CCW hull is
    // (0,0)->(1,0)->(1,1)->(0,1), so edge 0 is the fixed winner.
    std::vector<InputPoint> square = {
        {"sw", 0, 0}, {"se", 1, 0}, {"ne", 1, 1}, {"nw", 0, 1}};
    auto result = solve_minimum_bounding_rectangle("square", square, options);
    CHECK(result.status.ok());
    CHECK(result.solution->area == Rational(1));
    CHECK(result.solution->selected_edge_index == 0);
    CHECK(result.solution->direction == Point{Rational(1), Rational(0)});
    CHECK(result.solution->contacts.t_min == std::vector<std::string>({"nw", "sw"}));
    CHECK(result.solution->contacts.n_min == std::vector<std::string>({"ne", "nw"}));

    // Rectangle 2x1 cannot tie in side lengths; area is exactly 2 and the
    // first long hull edge wins.
    std::vector<InputPoint> rectangle = {
        {"a", 0, 0}, {"b", 2, 0}, {"c", 2, 1}, {"d", 0, 1}};
    auto rectangle_result =
        solve_minimum_bounding_rectangle("rectangle", rectangle, options);
    CHECK(rectangle_result.status.ok());
    CHECK(rectangle_result.solution->area == Rational(2));
    CHECK(rectangle_result.solution->selected_edge_index == 0);
    CHECK(rectangle_result.solution->direction ==
          Point{Rational(2), Rational(0)});
    return 0;
}
