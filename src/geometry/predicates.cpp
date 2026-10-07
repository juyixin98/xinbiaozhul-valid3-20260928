#include "geometry/predicates.hpp"

namespace mileage {

int orient2d(const Vec2& a, const Vec2& b, const Vec2& c) {
  const cpp_int v = cross(b - a, c - a);
  if (v > 0) return 1;
  if (v < 0) return -1;
  return 0;
}

bool on_segment_inclusive(const Vec2& a, const Vec2& b, const Vec2& p) {
  if (orient2d(a, b, p) != 0) return false;
  const Vec2 ap = p - a;
  const Vec2 ab = b - a;
  const cpp_int t = dot(ap, ab);
  if (t < 0) return false;
  return t <= dot(ab, ab);
}

const char* to_string(SegRelation r) {
  switch (r) {
    case SegRelation::Disjoint: return "Disjoint";
    case SegRelation::ProperCross: return "ProperCross";
    case SegRelation::EndpointTouch: return "EndpointTouch";
    case SegRelation::CollinearOverlap: return "CollinearOverlap";
    case SegRelation::CollinearTouch: return "CollinearTouch";
    case SegRelation::Identical: return "Identical";
  }
  return "?";
}

SegRelation segment_relation(const Vec2& a, const Vec2& b, const Vec2& c,
                             const Vec2& d) {
  const bool ab_point = (a == b);
  const bool cd_point = (c == d);
  if (ab_point && cd_point) return a == c ? SegRelation::Identical : SegRelation::Disjoint;
  if (ab_point) return on_segment_inclusive(c, d, a) ? SegRelation::EndpointTouch
                                                     : SegRelation::Disjoint;
  if (cd_point) return on_segment_inclusive(a, b, c) ? SegRelation::EndpointTouch
                                                     : SegRelation::Disjoint;

  const int o1 = orient2d(a, b, c);
  const int o2 = orient2d(a, b, d);
  const int o3 = orient2d(c, d, a);
  const int o4 = orient2d(c, d, b);

  if (o1 == 0 && o2 == 0) {
    // Collinear: classify by 1D overlap along the dominant axis.
    const bool use_x = (a.x != b.x);
    const cpp_int& a0 = use_x ? a.x : a.y;
    const cpp_int& b0 = use_x ? b.x : b.y;
    const cpp_int& c0 = use_x ? c.x : c.y;
    const cpp_int& d0 = use_x ? d.x : d.y;
    const cpp_int lo1 = a0 < b0 ? a0 : b0, hi1 = a0 < b0 ? b0 : a0;
    const cpp_int lo2 = c0 < d0 ? c0 : d0, hi2 = c0 < d0 ? d0 : c0;
    if (hi1 < lo2 || hi2 < lo1) return SegRelation::Disjoint;
    if ((a == c && b == d) || (a == d && b == c)) return SegRelation::Identical;
    if (hi1 == lo2 || hi2 == lo1) return SegRelation::CollinearTouch;
    return SegRelation::CollinearOverlap;
  }

  if (o1 != o2 && o3 != o4) return SegRelation::ProperCross;

  if ((o1 == 0 && on_segment_inclusive(a, b, c)) ||
      (o2 == 0 && on_segment_inclusive(a, b, d)) ||
      (o3 == 0 && on_segment_inclusive(c, d, a)) ||
      (o4 == 0 && on_segment_inclusive(c, d, b))) {
    return SegRelation::EndpointTouch;
  }
  return SegRelation::Disjoint;
}

Rat dist2_point_segment(const Vec2& p, const Vec2& a, const Vec2& b, Rat& t_out) {
  const Vec2 ab = b - a;
  const Vec2 ap = p - a;
  const cpp_int len2 = dot(ab, ab);
  if (len2 == 0) {
    // Zero-length segment: projection is the degenerate endpoint itself.
    t_out = Rat{cpp_int(0), cpp_int(1)};
    return Rat{dot(ap, ap), cpp_int(1)};
  }
  const cpp_int t_num = dot(ap, ab);
  if (t_num <= 0) {
    t_out = Rat{cpp_int(0), cpp_int(1)};
    return Rat{dot(ap, ap), cpp_int(1)};
  }
  if (t_num >= len2) {
    t_out = Rat{cpp_int(1), cpp_int(1)};
    const Vec2 bp = p - b;
    return Rat{dot(bp, bp), cpp_int(1)};
  }
  t_out = Rat{t_num, len2};
  // |ap|^2 - (ap.ab)^2 / |ab|^2, kept as one exact rational.
  return Rat{dot(ap, ap) * len2 - t_num * t_num, len2};
}

cpp_int dist2_point_aabb(const Vec2& p, const Vec2& lo, const Vec2& hi) {
  cpp_int dx = 0, dy = 0;
  if (p.x < lo.x) dx = lo.x - p.x;
  else if (p.x > hi.x) dx = p.x - hi.x;
  if (p.y < lo.y) dy = lo.y - p.y;
  else if (p.y > hi.y) dy = p.y - hi.y;
  return dx * dx + dy * dy;
}

}  // namespace mileage
