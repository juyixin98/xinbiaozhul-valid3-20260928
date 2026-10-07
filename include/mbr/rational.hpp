#pragma once

#include <boost/multiprecision/cpp_int.hpp>

#include <optional>
#include <string>
#include <compare>

namespace mbr {

using Integer = boost::multiprecision::cpp_int;

class Rational {
public:
    Rational();
    Rational(long long value);
    Rational(Integer numerator, Integer denominator);

    static std::optional<Rational> parse(std::string_view text);

    [[nodiscard]] const Integer& numerator() const noexcept { return num_; }
    [[nodiscard]] const Integer& denominator() const noexcept { return den_; }
    [[nodiscard]] int sign() const noexcept;
    [[nodiscard]] Rational abs_value() const;
    [[nodiscard]] Rational square() const;
    [[nodiscard]] Rational reciprocal() const;

    Rational& operator+=(const Rational& other);
    Rational& operator-=(const Rational& other);
    Rational& operator*=(const Rational& other);
    Rational& operator/=(const Rational& other);

    friend Rational operator+(Rational a, const Rational& b) { return a += b; }
    friend Rational operator-(Rational a, const Rational& b) { return a -= b; }
    friend Rational operator*(Rational a, const Rational& b) { return a *= b; }
    friend Rational operator/(Rational a, const Rational& b) { return a /= b; }
    friend Rational operator-(const Rational& value);

    friend bool operator==(const Rational& a, const Rational& b);
    friend std::strong_ordering operator<=>(const Rational& a,
                                           const Rational& b);

    [[nodiscard]] std::string to_string() const;
    [[nodiscard]] std::string to_fixed(int digits) const;
    [[nodiscard]] std::string to_fixed_upper(int digits) const;

private:
    Integer num_;
    Integer den_;

    void normalize();
};

Integer floor_division(const Integer& numerator, const Integer& positive_denominator);
Integer ceil_sqrt(const Integer& value);
Rational upper_sqrt(const Rational& value);

}  // namespace mbr
