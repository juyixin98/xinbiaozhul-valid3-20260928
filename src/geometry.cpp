#include "mbr/geometry.hpp"

#include <cmath>

namespace mbr {

Point operator+(const Point& a, const Point& b) {
    return {a.x + b.x, a.y + b.y};
}

Point operator-(const Point& a, const Point& b) {
    return {a.x - b.x, a.y - b.y};
}

Point operator*(const Point& a, const Rational& scale) {
    return {a.x * scale, a.y * scale};
}

bool operator==(const Point& a, const Point& b) {
    return a.x == b.x && a.y == b.y;
}

bool operator<(const Point& a, const Point& b) {
    if (a.x != b.x) return a.x < b.x;
    return a.y < b.y;
}

Rational dot(const Point& a, const Point& b) {
    return a.x * b.x + a.y * b.y;
}

Rational cross(const Point& a, const Point& b) {
    return a.x * b.y - a.y * b.x;
}

Rational squared_distance(const Point& a, const Point& b) {
    Point delta = b - a;
    return dot(delta, delta);
}

}  // namespace mbr
