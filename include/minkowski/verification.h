#pragma once

#include "minkowski/convex_hull.h"
#include "minkowski/types.h"

#include <string>

namespace minkowski {

struct VerificationReport {
    bool passed = false;
    std::string rule;
    std::string reason;
    ConvexSet expected;
    ConvexSet actual;
};

// Builds every pairwise vertex sum before applying the independent monotone
// chain hull. It deliberately does not call minkowski_sum.
ConvexSet reference_pairwise_sum(const ConvexSet& a, const ConvexSet& b);

VerificationReport verify_minkowski(const ConvexSet& sum,
                                    const ConvexSet& a,
                                    const ConvexSet& b);
VerificationReport verify_translation(const ConvexSet& translated,
                                      const ConvexSet& original,
                                      const Point& offset);
VerificationReport verify_support_additivity(const ConvexSet& a,
                                             const ConvexSet& b,
                                             const Point& direction);

} // namespace minkowski
