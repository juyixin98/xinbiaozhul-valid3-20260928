#pragma once

#include <boost/multiprecision/cpp_dec_float.hpp>
#include <boost/multiprecision/cpp_int.hpp>
#include <boost/multiprecision/rational_adaptor.hpp>

#include <cstddef>
#include <string>
#include <vector>

namespace nearest {

using Integer = boost::multiprecision::cpp_int;
using Rational = boost::multiprecision::number<
    boost::multiprecision::rational_adaptor<
        boost::multiprecision::cpp_int_backend<>>>;
using Decimal = boost::multiprecision::cpp_dec_float_50;

template <typename Scalar>
struct Point2 {
    Scalar x{};
    Scalar y{};
};

using RationalPoint = Point2<Rational>;
using DecimalPoint = Point2<Decimal>;

struct ProjectionCandidate {
    std::size_t segment_index = 0;
    RationalPoint point{};
    Rational t{};
    Rational distance_squared{};
};

struct QueryOutcome {
    std::vector<ProjectionCandidate> candidates;
    std::size_t nodes_visited = 0;
    std::size_t nodes_pruned = 0;
    std::size_t leaves_visited = 0;
};

enum class FailureCategory {
    None,
    EmptyPolyline,
    InsufficientVertices,
    InvalidCoordinate,
    InvalidVertexRecord,
    InvalidQueryRecord,
    ZeroLengthRejectedByConfiguration,
    UnsupportedConfigurationValue,
    InternalError
};

std::string failureName(FailureCategory category);

}  // namespace nearest
