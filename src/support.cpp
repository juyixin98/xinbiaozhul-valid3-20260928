#include "minkowski/support.h"

#include "minkowski/predicates.h"

#include <stdexcept>

namespace minkowski {

SupportResult support(const ConvexSet& set, const Point& direction) {
    if (set.kind == ShapeKind::Empty || set.vertices.empty()) {
        throw std::invalid_argument("support is undefined for an empty set");
    }
    if (direction.x == 0 && direction.y == 0) {
        return {0, set.vertices};
    }

    SupportResult result;
    bool initialized = false;
    for (const Point& p : set.vertices) {
        const Integer current = dot(p, direction);
        if (!initialized || current > result.value) {
            result.value = current;
            result.maximizers = {p};
            initialized = true;
        } else if (current == result.value) {
            result.maximizers.push_back(p);
        }
    }
    return result;
}

} // namespace minkowski
