#include "minkowski/verification.h"

#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"
#include "minkowski/support.h"

#include <sstream>

namespace minkowski {

ConvexSet reference_pairwise_sum(const ConvexSet& a, const ConvexSet& b) {
    if (a.kind == ShapeKind::Empty || b.kind == ShapeKind::Empty) return {};

    std::vector<Point> enumerated;
    enumerated.reserve(a.vertices.size() * b.vertices.size());
    for (const Point& pa : a.vertices) {
        for (const Point& pb : b.vertices) {
            enumerated.push_back(pa + pb);
        }
    }
    return convex_hull_from_points(std::move(enumerated));
}

VerificationReport verify_minkowski(const ConvexSet& sum,
                                    const ConvexSet& a,
                                    const ConvexSet& b) {
    VerificationReport report;
    report.rule = "enumerated-pairwise-convex-hull";
    report.expected = reference_pairwise_sum(a, b);
    report.actual = sum;
    report.passed = canonically_equal(report.expected, sum);
    if (!report.passed) {
        std::ostringstream reason;
        reason << "edge-merge result " << to_string(sum)
               << " differs from enumerated hull "
               << to_string(report.expected);
        report.reason = reason.str();
    }
    return report;
}

VerificationReport verify_translation(const ConvexSet& translated,
                                      const ConvexSet& original,
                                      const Point& offset) {
    VerificationReport report;
    report.rule = "Minkowski-sum-with-point-is-translation";
    report.expected = translate(original, offset);
    report.actual = translated;
    report.passed = canonically_equal(report.expected, translated);
    if (!report.passed) report.reason = "canonical translated sets differ";
    return report;
}

VerificationReport verify_support_additivity(const ConvexSet& a,
                                             const ConvexSet& b,
                                             const Point& direction) {
    VerificationReport report;
    report.rule = "h_A+h_B=h_(A+B)";
    const ConvexSet sum = minkowski_sum(a, b);
    const auto ha = support(a, direction);
    const auto hb = support(b, direction);
    const auto hs = support(sum, direction);

    report.actual = sum;
    report.expected = sum;
    report.passed = ha.value + hb.value == hs.value;

    std::ostringstream reason;
    reason << "h_A=" << ha.value.str()
           << " h_B=" << hb.value.str()
           << " h_sum=" << hs.value.str();
    if (!report.passed) {
        reason << " values do not add for direction " << to_string(direction);
    }
    report.reason = reason.str();
    return report;
}

} // namespace minkowski
