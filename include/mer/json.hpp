#pragma once

#include <string>
#include <string_view>

namespace mer {

std::string json_escape(std::string_view value);

}  // namespace mer
