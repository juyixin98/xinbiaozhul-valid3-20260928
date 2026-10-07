#pragma once

#include "mer/types.hpp"

namespace mer {

// Exact predicate. Positive means a->b->c is counter-clockwise. This is the
// only orientation result used by hull and optimization decisions.
Integer exact_orientation(const Point& a, const Point& b, const Point& c);

// Diagnostic-only near-collinearity. It never changes the exact algorithm.
// The predicate tests |cross| / |b-a| <= tolerance, using squared quantities:
// cross^2 <= tolerance^2 * |b-a|^2. A zero-length reference segment is an
// input/state problem rather than a geometric zero divide.
NearCollinearReport near_collinear(const Point& a,
                                   const Point& b,
                                   const Point& c,
                                   const Decimal& tolerance);

}  // namespace mer
