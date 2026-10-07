#include "mbr/solver.hpp"
#include "test_support.hpp"

#include <algorithm>
#include <set>
#include <vector>

namespace {

using namespace mbr;

struct ReferenceCandidate {
    std::string start_id;
    std::string end_id;
    Rational area;
    Rational perimeter_key;
};

Rational reference_area(const std::vector<InputPoint>& points,
                        const Point& direction) {
    Rational length_squared =
        direction.x.square() + direction.y.square();
    bool initialized = false;
    Rational min_dot, max_dot, min_cross, max_cross;
    for (const InputPoint& point : points) {
        Rational dot_value = point.x * direction.x + point.y * direction.y;
        Rational cross_value =
            point.x * direction.y - point.y * direction.x;
        if (!initialized) {
            min_dot = max_dot = dot_value;
            min_cross = max_cross = cross_value;
            initialized = true;
        }
        min_dot = std::min(min_dot, dot_value);
        max_dot = std::max(max_dot, dot_value);
        min_cross = std::min(min_cross, cross_value);
        max_cross = std::max(max_cross, cross_value);
    }
    return (max_dot - min_dot) * (max_cross - min_cross) / length_squared;
}

Rational reference_perimeter_key(const std::vector<InputPoint>& points,
                                 const Point& direction) {
    Rational length_squared =
        direction.x.square() + direction.y.square();
    bool initialized = false;
    Rational min_dot, max_dot, min_cross, max_cross;
    for (const InputPoint& point : points) {
        Rational dot_value = point.x * direction.x + point.y * direction.y;
        Rational cross_value =
            point.x * direction.y - point.y * direction.x;
        if (!initialized) {
            min_dot = max_dot = dot_value;
            min_cross = max_cross = cross_value;
            initialized = true;
        }
        min_dot = std::min(min_dot, dot_value);
        max_dot = std::max(max_dot, dot_value);
        min_cross = std::min(min_cross, cross_value);
        max_cross = std::max(max_cross, cross_value);
    }
    return (max_dot - min_dot + max_cross - min_cross).square() /
           length_squared;
}

std::set<std::string> id_set(const std::vector<std::string>& ids) {
    return std::set<std::string>(ids.begin(), ids.end());
}

bool covers_all_contact_ids(const std::vector<std::string>& contacts,
                            const std::set<std::string>& expected) {
    return id_set(contacts) == expected;
}

}  // namespace

int main() {
    using namespace mbr;
    SolverOptions options;

    // A right triangle: (0,0),(4,0),(0,3). The independent oracle checks
    // every unordered point-pair direction, including the hypotenuse.
    std::vector<InputPoint> triangle = {
        {"o", 0, 0}, {"x", 4, 0}, {"y", 0, 3}};
    SolveResult solved =
        solve_minimum_bounding_rectangle("triangle", triangle, options);
    CHECK(solved.status.ok());

    Rational oracle_minimum;
    Rational oracle_perimeter_key;
    bool initialized = false;
    for (std::size_t i = 0; i < triangle.size(); ++i) {
        for (std::size_t j = i + 1; j < triangle.size(); ++j) {
            Point direction{
                triangle[j].x - triangle[i].x,
                triangle[j].y - triangle[i].y};
            Rational area = reference_area(triangle, direction);
            Rational perimeter_key =
                reference_perimeter_key(triangle, direction);
            if (!initialized || area < oracle_minimum ||
                (area == oracle_minimum &&
                 perimeter_key < oracle_perimeter_key)) {
                oracle_minimum = area;
                oracle_perimeter_key = perimeter_key;
                initialized = true;
            }
        }
    }
    CHECK(solved.solution->area == oracle_minimum);
    CHECK(solved.solution->direction_evaluations.size() == 3);
    for (const DirectionEvaluation& evaluation :
         solved.solution->direction_evaluations) {
        Point direction{
            evaluation.edge_end.x - evaluation.edge_start.x,
            evaluation.edge_end.y - evaluation.edge_start.y};
        Rational independent_area =
            reference_area(triangle, direction);
        CHECK(evaluation.area == independent_area);
        CHECK(independent_area >= oracle_minimum);
    }

    // Reconstructed corner inclusion for every input point:
    // 0 <= dot(q,d)-dot_min and the analogous three inequalities.
    const RectangleSolution& solution = *solved.solution;
    Rational selected_length_squared =
        solution.direction.x.square() + solution.direction.y.square();
    Rational independent_dot_min;
    Rational independent_dot_max;
    Rational independent_normal_min;
    Rational independent_normal_max;
    bool bounds_initialized = false;
    for (const InputPoint& point : triangle) {
        Rational dot_value = point.x * solution.direction.x +
                             point.y * solution.direction.y;
        Rational normal_value = point.x * solution.left_normal.x +
                                point.y * solution.left_normal.y;
        if (!bounds_initialized) {
            independent_dot_min = independent_dot_max = dot_value;
            independent_normal_min = independent_normal_max = normal_value;
            bounds_initialized = true;
        }
        independent_dot_min = std::min(independent_dot_min, dot_value);
        independent_dot_max = std::max(independent_dot_max, dot_value);
        independent_normal_min =
            std::min(independent_normal_min, normal_value);
        independent_normal_max =
            std::max(independent_normal_max, normal_value);
    }
    for (const InputPoint& point : triangle) {
        Rational dot_value = point.x * solution.direction.x +
                             point.y * solution.direction.y;
        Rational normal_value = point.x * solution.left_normal.x +
                                point.y * solution.left_normal.y;
        CHECK(dot_value >= independent_dot_min);
        CHECK(dot_value <= independent_dot_max);
        CHECK(normal_value >= independent_normal_min);
        CHECK(normal_value <= independent_normal_max);
    }
    auto expected_corner = [&](const Rational& dot_value,
                               const Rational& normal_value) {
        return Point{
            (dot_value * solution.direction.x +
             normal_value * solution.left_normal.x) /
                selected_length_squared,
            (dot_value * solution.direction.y +
             normal_value * solution.left_normal.y) /
                selected_length_squared};
    };
    CHECK(solution.corners[0] == expected_corner(
        independent_dot_min, independent_normal_min));
    CHECK(solution.corners[1] == expected_corner(
        independent_dot_max, independent_normal_min));
    CHECK(solution.corners[2] == expected_corner(
        independent_dot_max, independent_normal_max));
    CHECK(solution.corners[3] == expected_corner(
        independent_dot_min, independent_normal_max));

    // A square with a collinear boundary point and interior point verifies
    // that contact evidence includes every coincident-boundary point.
    std::vector<InputPoint> square = {
        {"a", 0, 0}, {"b", 4, 0}, {"c", 4, 3}, {"d", 0, 3},
        {"e", 2, 1}, {"f", 2, 0}};
    auto square_result =
        solve_minimum_bounding_rectangle("square", square, options);
    CHECK(square_result.status.ok());
    CHECK(square_result.solution->area == Rational(12));
    CHECK(covers_all_contact_ids(
        square_result.solution->contacts.t_min, {"a", "b", "f"}));
    CHECK(covers_all_contact_ids(
        square_result.solution->contacts.t_max, {"c", "d"}));
    CHECK(covers_all_contact_ids(
        square_result.solution->contacts.n_min, {"b", "c"}));
    CHECK(covers_all_contact_ids(
        square_result.solution->contacts.n_max, {"a", "d"}));

    Rational independent_square_area;
    bool square_initialized = false;
    for (std::size_t i = 0; i < square.size(); ++i) {
        for (std::size_t j = i + 1; j < square.size(); ++j) {
            Point direction{square[j].x - square[i].x,
                            square[j].y - square[i].y};
            if (direction.x == Rational(0) && direction.y == Rational(0)) {
                continue;
            }
            Rational area = reference_area(square, direction);
            if (!square_initialized || area < independent_square_area) {
                independent_square_area = area;
                square_initialized = true;
            }
        }
    }
    CHECK(square_result.solution->area == independent_square_area);
    return 0;
}
