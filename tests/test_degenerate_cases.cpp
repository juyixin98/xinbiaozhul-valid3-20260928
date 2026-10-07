#include "mbr/solver.hpp"
#include "test_support.hpp"

#include <set>

int main() {
    using namespace mbr;
    SolverOptions options;

    auto one = solve_minimum_bounding_rectangle("one", {
        InputPoint{"p", Rational(7, 2), Rational(-5, 4)}
    }, options);
    CHECK(one.status.ok());
    CHECK(one.solution->degenerate_kind == "single_point");
    CHECK(one.solution->area == Rational(0));
    CHECK(one.solution->corners[0] == one.solution->corners[3]);
    CHECK(one.solution->contacts.t_min.size() == 1);
    CHECK(one.solution->contacts.n_max.at(0) == "p");

    std::vector<InputPoint> collinear = {
        {"a", Rational(-3), Rational(1)},
        {"b", Rational(0), Rational(1)},
        {"c", Rational(2), Rational(1)},
        {"d", Rational(0), Rational(1)}
    };
    auto line = solve_minimum_bounding_rectangle("line", collinear, options);
    CHECK(line.status.ok());
    CHECK(line.solution->degenerate_kind == "collinear");
    CHECK(line.solution->hull_vertex_count == 2);
    CHECK(line.solution->area == Rational(0));
    CHECK(line.solution->side_perpendicular == Rational(0));
    CHECK(line.solution->selected_edge_index == 0);
    CHECK(line.solution->direction == Point{Rational(5), Rational(0)});

    std::set<std::string> both_perp(
        line.solution->contacts.n_min.begin(),
        line.solution->contacts.n_min.end());
    for (const std::string& id : {"a", "b", "c", "d"}) {
        CHECK(both_perp.count(id) == 1);
    }
    return 0;
}
