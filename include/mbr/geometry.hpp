#pragma once

#include "mbr/rational.hpp"

#include <array>
#include <string>
#include <vector>

namespace mbr {

struct Point {
    Rational x;
    Rational y;
};

struct InputPoint {
    std::string id;
    Rational x;
    Rational y;
};

struct HullPoint {
    Point coordinate;
    std::vector<std::string> source_ids;
};

Point operator+(const Point& a, const Point& b);
Point operator-(const Point& a, const Point& b);
Point operator*(const Point& a, const Rational& scale);
bool operator==(const Point& a, const Point& b);
bool operator<(const Point& a, const Point& b);

Rational dot(const Point& a, const Point& b);
Rational cross(const Point& a, const Point& b);
Rational squared_distance(const Point& a, const Point& b);

}  // namespace mbr
