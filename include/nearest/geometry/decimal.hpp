#pragma once

#include "nearest/geometry/types.hpp"

#include <optional>
#include <string>

namespace nearest {

std::optional<Rational> parseDecimalRational(const std::string& text);
Decimal rationalToDecimal(const Rational& value);
std::string formatDecimal(const Decimal& value);
std::string formatRational(const Rational& value);

}  // namespace nearest
