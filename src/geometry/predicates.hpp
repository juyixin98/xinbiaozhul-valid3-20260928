#pragma once

#include "geometry/types.hpp"

namespace mileage {

// Sign of the oriented area of triangle (a, b, c): +1 counter-clockwise,
// -1 clockwise, 0 collinear. Exact for arbitrary coordinate magnitudes.
int orient2d(const Vec2& a, const Vec2& b, const Vec2& c);

bool on_segment_inclusive(const Vec2& a, const Vec2& b, const Vec2& p);

enum class SegRelation {
  Disjoint,
  ProperCross,      // interiors cross at a single point
  EndpointTouch,    // one endpoint lies on the other segment (non-collinear)
  CollinearOverlap, // collinear with overlapping interiors
  CollinearTouch,   // collinear, touching at exactly one endpoint
  Identical         // same endpoints (in either order)
};

const char* to_string(SegRelation r);

// Exact segment/segment relation. Zero-length segments are treated as points.
SegRelation segment_relation(const Vec2& a, const Vec2& b, const Vec2& c,
                             const Vec2& d);

// Exact squared distance from p to segment [a,b]; also yields the exact
// clamped parameter t in [0,1] of the closest point. Zero-length segments
// return t = 0 and the plain point distance.
Rat dist2_point_segment(const Vec2& p, const Vec2& a, const Vec2& b, Rat& t_out);

// Exact squared distance from p to the axis-aligned box [lo,hi].
cpp_int dist2_point_aabb(const Vec2& p, const Vec2& lo, const Vec2& hi);

}  // namespace mileage
