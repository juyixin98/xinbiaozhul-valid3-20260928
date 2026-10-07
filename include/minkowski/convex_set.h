#pragma once

#include "minkowski/types.h"

#include <stdexcept>
#include <string>

namespace minkowski {

enum class ValidationError {
    EmptyInput,
    DuplicateVertex,
    NonConvex,
    SelfIntersectionOrRepeatedTraversal
};

std::string to_string(ValidationError error);

struct ValidationResult {
    bool ok = false;
    ConvexSet value;
    ValidationError error = ValidationError::EmptyInput;
};

// Accepts CW or CCW convex polygons. It intentionally rejects points repeated
// in the input and reports concave/self-crossing traversals as NonConvex.
ValidationResult validate_and_normalize(const std::vector<Point>& input);

bool canonically_equal(const ConvexSet& a, const ConvexSet& b);
ConvexSet make_point(Point p);
ConvexSet make_segment(Point a, Point b);

} // namespace minkowski
