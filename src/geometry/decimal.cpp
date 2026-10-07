#include "nearest/geometry/decimal.hpp"

#include <boost/multiprecision/cpp_dec_float.hpp>

#include <algorithm>
#include <cctype>
#include <iomanip>
#include <sstream>

namespace nearest {

std::string failureName(FailureCategory category) {
    switch (category) {
        case FailureCategory::None:
            return "NONE";
        case FailureCategory::EmptyPolyline:
            return "EMPTY_POLYLINE";
        case FailureCategory::InsufficientVertices:
            return "INSUFFICIENT_VERTICES";
        case FailureCategory::InvalidCoordinate:
            return "INVALID_COORDINATE";
        case FailureCategory::InvalidVertexRecord:
            return "INVALID_VERTEX_RECORD";
        case FailureCategory::InvalidQueryRecord:
            return "INVALID_QUERY_RECORD";
        case FailureCategory::ZeroLengthRejectedByConfiguration:
            return "ZERO_LENGTH_REJECTED_BY_CONFIGURATION";
        case FailureCategory::UnsupportedConfigurationValue:
            return "UNSUPPORTED_CONFIGURATION_VALUE";
        case FailureCategory::InternalError:
            return "INTERNAL_ERROR";
    }
    return "INTERNAL_ERROR";
}

namespace {

void appendDigits(Integer& target, const std::string& digits) {
    for (char c : digits) {
        target *= 10;
        target += c - '0';
    }
}

Integer tenPower(unsigned exponent) {
    Integer result = 1;
    for (unsigned i = 0; i < exponent; ++i) {
        result *= 10;
    }
    return result;
}

}  // namespace

std::optional<Rational> parseDecimalRational(const std::string& raw) {
    std::string text = raw;
    if (text.empty()) {
        return std::nullopt;
    }

    std::size_t sign_size = (text.front() == '+' || text.front() == '-') ? 1U : 0U;
    bool negative = sign_size == 1U && text.front() == '-';
    std::string body = text.substr(sign_size);
    if (body.empty()) {
        return std::nullopt;
    }

    int exponent = 0;
    if (auto e = body.find_first_of("eE"); e != std::string::npos) {
        if (e + 1U == body.size()) {
            return std::nullopt;
        }
        bool exp_negative = false;
        std::size_t exponent_start = e + 1U;
        if (body[exponent_start] == '+' || body[exponent_start] == '-') {
            exp_negative = body[exponent_start] == '-';
            ++exponent_start;
        }
        if (exponent_start == body.size()) {
            return std::nullopt;
        }
        int parsed_exponent = 0;
        for (std::size_t i = exponent_start; i < body.size(); ++i) {
            if (!std::isdigit(static_cast<unsigned char>(body[i]))) {
                return std::nullopt;
            }
            parsed_exponent = parsed_exponent * 10 + (body[i] - '0');
        }
        exponent = exp_negative ? -parsed_exponent : parsed_exponent;
        body.erase(e);
    }

    std::string integer_part = "0";
    std::string fractional_part;
    if (auto dot = body.find('.'); dot != std::string::npos) {
        if (dot != 0U) {
            integer_part = body.substr(0, dot);
        }
        fractional_part = body.substr(dot + 1U);
    } else {
        integer_part = body;
    }

    if (integer_part.empty() || !std::all_of(integer_part.begin(), integer_part.end(),
            [](unsigned char c) { return std::isdigit(c); })) {
        return std::nullopt;
    }
    if (!std::all_of(fractional_part.begin(), fractional_part.end(),
            [](unsigned char c) { return std::isdigit(c); })) {
        return std::nullopt;
    }

    Integer numerator = 0;
    appendDigits(numerator, integer_part);
    Integer fractional_value = 0;
    appendDigits(fractional_value, fractional_part);
    numerator *= tenPower(static_cast<unsigned>(fractional_part.size()));
    numerator += fractional_value;
    if (negative) {
        numerator = -numerator;
    }

    int power = exponent - static_cast<int>(fractional_part.size());
    Rational value(numerator, 1);
    if (power >= 0) {
        value *= Rational(tenPower(static_cast<unsigned>(power)), 1);
    } else {
        value /= Rational(tenPower(static_cast<unsigned>(-power)), 1);
    }
    return value;
}

Decimal rationalToDecimal(const Rational& value) {
    using boost::multiprecision::denominator;
    using boost::multiprecision::numerator;
    Decimal n(numerator(value).convert_to<std::string>());
    Decimal d(denominator(value).convert_to<std::string>());
    return n / d;
}

std::string formatDecimal(const Decimal& value) {
    std::stringstream out;
    out << std::setprecision(std::numeric_limits<Decimal>::digits10 + 1)
        << std::fixed << value;
    std::string text = out.str();
    if (text.find('.') != std::string::npos) {
        while (text.size() > 1U && text.back() == '0') {
            text.pop_back();
        }
        if (text.back() == '.') {
            text.pop_back();
        }
    }
    return text;
}

std::string formatRational(const Rational& value) {
    std::stringstream out;
    using boost::multiprecision::denominator;
    using boost::multiprecision::numerator;
    out << numerator(value) << '/' << denominator(value);
    return out.str();
}

}  // namespace nearest
