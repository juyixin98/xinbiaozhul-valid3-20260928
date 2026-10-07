#include "mer/predicates.hpp"

#include "mer/errors.hpp"

#include <boost/multiprecision/cpp_int.hpp>

namespace mer {

Integer exact_orientation(const Point& a, const Point& b, const Point& c) {
  return cross(a, b, c);
}

NearCollinearReport near_collinear(const Point& a,
                                   const Point& b,
                                   const Point& c,
                                   const Decimal& tolerance) {
  if (tolerance < 0) {
    throw MerError(ErrorCategory::InputError, "NEGATIVE_TOLERANCE",
                   "near-collinear tolerance must be non-negative");
  }
  Integer dx = integer_from_coord(b.x) - integer_from_coord(a.x);
  Integer dy = integer_from_coord(b.y) - integer_from_coord(a.y);
  Integer len2 = dx * dx + dy * dy;
  if (len2 == 0) {
    throw MerError(ErrorCategory::StateConflict, "ZERO_REFERENCE_SEGMENT",
                   "cannot evaluate near-collinearity against a zero segment");
  }

  Integer cr = exact_orientation(a, b, c);
  Decimal residual = decimal_from_integer(boost::multiprecision::abs(cr)) /
                     boost::multiprecision::sqrt(decimal_from_integer(len2));
  return {
      residual <= tolerance,
      cr,
      len2,
      residual,
      tolerance,
  };
}

}  // namespace mer
