#include "minkowski/types.h"

#include <stdexcept>

namespace minkowski {

Point operator+(const Point& left, const Point& right) {
    return {left.x + right.x, left.y + right.y};
}

Point operator-(const Point& left, const Point& right) {
    return {left.x - right.x, left.y - right.y};
}

Point& operator+=(Point& left, const Point& right) {
    left.x += right.x;
    left.y += right.y;
    return left;
}

Point& operator-=(Point& left, const Point& right) {
    left.x -= right.x;
    left.y -= right.y;
    return left;
}

Integer dot(const Point& a, const Point& b) {
    return a.x * b.x + a.y * b.y;
}

Integer cross(const Point& a, const Point& b) {
    return a.x * b.y - a.y * b.x;
}

Integer cross(const Point& a, const Point& b, const Point& c) {
    return cross(b - a, c - a);
}

std::string to_string(ShapeKind kind) {
    switch (kind) {
    case ShapeKind::Empty:
        return "empty";
    case ShapeKind::Point:
        return "point";
    case ShapeKind::Segment:
        return "segment";
    case ShapeKind::Polygon:
        return "polygon";
    }
    return "unknown";
}

std::string to_string(const Point& point) {
    return "(" + point.x.str() + "," + point.y.str() + ")";
}

std::string to_string(const ConvexSet& set) {
    std::string result = to_string(set.kind) + " " +
                         std::to_string(set.vertices.size()) + " ";
    for (std::size_t i = 0; i < set.vertices.size(); ++i) {
        if (i != 0) result += " ";
        result += set.vertices[i].x.str() + " " + set.vertices[i].y.str();
    }
    return result;
}

} // namespace minkowski
