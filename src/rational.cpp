#include "mbr/rational.hpp"

#include <compare>
#include <cctype>
#include <cmath>
#include <stdexcept>

namespace mbr {
namespace {

void skip_whitespace(std::string_view text, std::size_t& pos) {
    while (pos < text.size() &&
           std::isspace(static_cast<unsigned char>(text[pos]))) {
        ++pos;
    }
}

Integer power_of_ten(int exponent) {
    Integer result = 1;
    for (int i = 0; i < exponent; ++i) {
        result *= 10;
    }
    return result;
}

}  // namespace

Rational::Rational() : num_(0), den_(1) {}
Rational::Rational(long long value) : num_(value), den_(1) {}

namespace {

std::optional<Rational> parse_decimal_or_scientific(std::string_view text);

std::optional<Rational> parse_fraction_or_decimal(std::string_view text) {
    std::size_t slash = text.find('/');
    if (slash == std::string_view::npos) {
        return parse_decimal_or_scientific(text);
    }
    if (text.find('/', slash + 1) != std::string_view::npos) {
        return std::nullopt;
    }
    auto numerator =
        parse_decimal_or_scientific(text.substr(0, slash));
    auto denominator =
        parse_decimal_or_scientific(text.substr(slash + 1));
    if (!numerator || !denominator ||
        denominator->denominator() != 1 ||
        denominator->numerator() == 0) {
        return std::nullopt;
    }
    return *numerator / *denominator;
}

}

Rational::Rational(Integer numerator, Integer denominator)
    : num_(std::move(numerator)), den_(std::move(denominator)) {
    normalize();
}

std::optional<Rational> Rational::parse(std::string_view text) {
    return parse_fraction_or_decimal(text);
}

namespace {

std::optional<Rational> parse_decimal_or_scientific(std::string_view text) {
    std::size_t pos = 0;
    skip_whitespace(text, pos);
    if (pos >= text.size()) {
        return std::nullopt;
    }

    bool negative = false;
    if (text[pos] == '+' || text[pos] == '-') {
        negative = text[pos] == '-';
        ++pos;
    }

    const std::size_t digits_start = pos;
    while (pos < text.size() && std::isdigit(static_cast<unsigned char>(text[pos]))) {
        ++pos;
    }

    Integer whole = 0;
    if (pos != digits_start) {
        whole = Integer(text.substr(digits_start, pos - digits_start));
    }

    Integer fraction = 0;
    int decimal_digits = 0;
    bool has_digits = pos != digits_start;

    if (pos < text.size() && text[pos] == '.') {
        ++pos;
        const std::size_t fraction_start = pos;
        while (pos < text.size() &&
               std::isdigit(static_cast<unsigned char>(text[pos]))) {
            ++pos;
        }
        decimal_digits = static_cast<int>(pos - fraction_start);
        if (decimal_digits > 0) {
            fraction = Integer(text.substr(fraction_start, decimal_digits));
            has_digits = true;
        }
    }

    if (!has_digits) {
        return std::nullopt;
    }

    Integer numerator = whole * power_of_ten(decimal_digits) + fraction;
    Integer denominator = power_of_ten(decimal_digits);

    int exponent = 0;
    if (pos < text.size() && (text[pos] == 'e' || text[pos] == 'E')) {
        ++pos;
        bool negative_exponent = false;
        if (pos < text.size() && (text[pos] == '+' || text[pos] == '-')) {
            negative_exponent = text[pos] == '-';
            ++pos;
        }
        const std::size_t exponent_start = pos;
        while (pos < text.size() &&
               std::isdigit(static_cast<unsigned char>(text[pos]))) {
            ++pos;
        }
        if (pos == exponent_start) {
            return std::nullopt;
        }
        exponent = std::stoi(std::string(text.substr(exponent_start,
                                                     pos - exponent_start)));
        if (negative_exponent) {
            exponent = -exponent;
        }
    }

    if (exponent >= 0) {
        numerator *= power_of_ten(exponent);
    } else {
        denominator *= power_of_ten(-exponent);
    }

    skip_whitespace(text, pos);
    if (pos != text.size()) {
        return std::nullopt;
    }

    if (negative) {
        numerator = -numerator;
    }
    return Rational(numerator, denominator);
}

}  // namespace

int Rational::sign() const noexcept {
    if (num_ == 0) return 0;
    return num_ < 0 ? -1 : 1;
}

Rational Rational::abs_value() const {
    Rational copy = *this;
    if (copy.num_ < 0) copy.num_ = -copy.num_;
    return copy;
}

Rational Rational::square() const {
    return Rational(num_ * num_, den_ * den_);
}

Rational Rational::reciprocal() const {
    if (num_ == 0) {
        throw std::domain_error("reciprocal of zero rational");
    }
    return Rational(den_, num_);
}

Rational& Rational::operator+=(const Rational& other) {
    num_ = num_ * other.den_ + other.num_ * den_;
    den_ *= other.den_;
    normalize();
    return *this;
}

Rational& Rational::operator-=(const Rational& other) {
    num_ = num_ * other.den_ - other.num_ * den_;
    den_ *= other.den_;
    normalize();
    return *this;
}

Rational& Rational::operator*=(const Rational& other) {
    num_ *= other.num_;
    den_ *= other.den_;
    normalize();
    return *this;
}

Rational& Rational::operator/=(const Rational& other) {
    if (other.num_ == 0) {
        throw std::domain_error("division by zero rational");
    }
    num_ *= other.den_;
    den_ *= other.num_;
    normalize();
    return *this;
}

Rational operator-(const Rational& value) {
    return Rational(-value.numerator(), value.denominator());
}

bool operator==(const Rational& a, const Rational& b) {
    return a.numerator() == b.numerator() &&
           a.denominator() == b.denominator();
}

std::strong_ordering operator<=>(const Rational& a, const Rational& b) {
    Integer left = a.numerator() * b.denominator();
    Integer right = b.numerator() * a.denominator();
    if (left < right) return std::strong_ordering::less;
    if (left > right) return std::strong_ordering::greater;
    return std::strong_ordering::equal;
}

std::string Rational::to_string() const {
    if (den_ == 1) {
    return num_.str();
    }
    return num_.str() + "/" + den_.str();
}

std::string Rational::to_fixed(int digits) const {
    if (digits < 0 || digits > 1000) {
        throw std::invalid_argument("fixed digits out of range");
    }
    Integer absolute_numerator = num_ < 0 ? -num_ : num_;
    Integer scaled_abs = absolute_numerator * power_of_ten(digits);
    Integer q = floor_division(scaled_abs, den_);
    Integer twice_remainder = 2 * (scaled_abs - q * den_);
    if (twice_remainder >= den_) {
        ++q;
    }

    std::string digits_text = q.str();
    if (digits_text.size() <= static_cast<std::size_t>(digits)) {
        digits_text.insert(digits_text.begin(),
                           static_cast<std::size_t>(digits) + 1 -
                               digits_text.size(),
                           '0');
    }
    std::string result;
    if (num_ < 0 && q != 0) {
        result.push_back('-');
    }
    result.append(digits_text.data(), digits_text.size() - digits);
    if (digits > 0) {
        result.push_back('.');
        result.append(digits_text.end() - digits, digits_text.end());
    }
    return result;
}

std::string Rational::to_fixed_upper(int digits) const {
    if (digits < 0 || digits > 1000) {
        throw std::invalid_argument("fixed digits out of range");
    }
    if (sign() < 0) {
        // Error bounds in this project are non-negative. For a negative
        // value, truncating toward zero is still an upper bound.
        Integer absolute = -num_;
        Integer scaled = absolute * power_of_ten(digits);
        Integer quotient = scaled / den_;
        std::string digits_text = quotient.str();
        if (digits_text.size() <= static_cast<std::size_t>(digits)) {
            digits_text.insert(digits_text.begin(),
                               static_cast<std::size_t>(digits) + 1 -
                                   digits_text.size(),
                               '0');
        }
        std::string negative_result = "-";
        negative_result.append(digits_text.data(),
                               digits_text.size() - digits);
        if (digits > 0) {
            negative_result.push_back('.');
            negative_result.append(digits_text.end() - digits,
                                   digits_text.end());
        }
        return negative_result;
    }
    Integer scaled = num_ * power_of_ten(digits);
    Integer quotient = scaled / den_;
    if (scaled % den_ != 0) {
        ++quotient;
    }
    std::string digits_text = quotient.str();
    if (digits_text.size() <= static_cast<std::size_t>(digits)) {
        digits_text.insert(digits_text.begin(),
                           static_cast<std::size_t>(digits) + 1 -
                               digits_text.size(),
                           '0');
    }
    std::string result;
    result.append(digits_text.data(), digits_text.size() - digits);
    if (digits > 0) {
        result.push_back('.');
        result.append(digits_text.end() - digits, digits_text.end());
    }
    return result;
}

void Rational::normalize() {
    if (den_ == 0) {
        throw std::domain_error("zero rational denominator");
    }
    if (den_ < 0) {
        num_ = -num_;
        den_ = -den_;
    }
    if (num_ == 0) {
        den_ = 1;
        return;
    }
    Integer absolute_numerator = num_ < 0 ? -num_ : num_;
    Integer common = boost::multiprecision::gcd(absolute_numerator, den_);
    num_ /= common;
    den_ /= common;
}

Integer floor_division(const Integer& numerator,
                       const Integer& positive_denominator) {
    if (positive_denominator <= 0) {
        throw std::invalid_argument("floor division requires positive denominator");
    }
    Integer quotient = numerator / positive_denominator;
    Integer remainder = numerator % positive_denominator;
    if (remainder != 0 && numerator < 0) {
        --quotient;
    }
    return quotient;
}

Integer ceil_sqrt(const Integer& value) {
    if (value < 0) {
        throw std::domain_error("square root of negative integer");
    }
    if (value == 0) {
        return 0;
    }

    Integer low = 1;
    Integer high = 1;
    while (high * high < value) {
        low = high;
        high *= 2;
    }
    while (low < high) {
        Integer middle = (low + high) / 2;
        if (middle * middle < value) {
            low = middle + 1;
        } else {
            high = middle;
        }
    }
    return low;
}

Rational upper_sqrt(const Rational& value) {
    if (value.sign() < 0) {
        throw std::domain_error("square root of negative rational");
    }
    // ceil(sqrt(num/den)) = ceil(sqrt(num*den))/den.
    Integer root = ceil_sqrt(value.numerator() * value.denominator());
    return Rational(root, value.denominator());
}

}  // namespace mbr
