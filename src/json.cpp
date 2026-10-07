#include "mer/json.hpp"

namespace mer {

std::string json_escape(std::string_view value) {
  std::string result;
  result.reserve(value.size() + 2);
  for (char c : value) {
    switch (c) {
      case '"':
        result += "\\\"";
        break;
      case '\\':
        result += "\\\\";
        break;
      case '\b':
        result += "\\b";
        break;
      case '\f':
        result += "\\f";
        break;
      case '\n':
        result += "\\n";
        break;
      case '\r':
        result += "\\r";
        break;
      case '\t':
        result += "\\t";
        break;
      default:
        if (static_cast<unsigned char>(c) < 0x20) {
          static const char* hex = "0123456789abcdef";
          result += "\\u00";
          result += hex[(c >> 4) & 0xf];
          result += hex[c & 0xf];
        } else {
          result += c;
        }
    }
  }
  return result;
}

}  // namespace mer
