#pragma once

#include <boost/multiprecision/cpp_int.hpp>

#include <compare>
#include <string>
#include <vector>

namespace minkowski {

using Integer = boost::multiprecision::cpp_int;

struct Point {
    Integer x{0};
    Integer y{0};

    friend auto operator<=>(const Point&, const Point&) = default;
    bool operator==(const Point&) const = default;
};

Point operator+(const Point& left, const Point& right);
Point operator-(const Point& left, const Point& right);
Point& operator+=(Point& left, const Point& right);
Point& operator-=(Point& left, const Point& right);

Integer dot(const Point& a, const Point& b);
Integer cross(const Point& a, const Point& b);
Integer cross(const Point& a, const Point& b, const Point& c);

enum class ShapeKind {
    Empty,
    Point,
    Segment,
    Polygon
};

std::string to_string(ShapeKind kind);

struct ConvexSet {
    ShapeKind kind = ShapeKind::Empty;
    // Empty has no vertices. Point has one. Segment has its two endpoints.
    // Polygon vertices are counter-clockwise, beginning at the lexicographic
    // minimum. Collinear boundary points are omitted.
    std::vector<Point> vertices;
};

std::string to_string(const Point& point);
std::string to_string(const ConvexSet& set);

} // namespace minkowski
