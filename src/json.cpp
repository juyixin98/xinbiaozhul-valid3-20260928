#include "mbr/json.hpp"

#include <cctype>

namespace mbr {
namespace {

class Parser {
public:
    explicit Parser(std::string_view text) : text_(text) {}

    JsonParseResult parse() {
        skip_ws();
        JsonValue root;
        if (!parse_value(root)) {
            return {false, error_, JsonValue{}};
        }
        skip_ws();
        if (pos_ != text_.size()) {
            return {false, make_error("trailing characters"), JsonValue{}};
        }
        return {true, "", root};
    }

private:
    std::string_view text_;
    std::size_t pos_ = 0;
    std::string error_;

    [[nodiscard]] std::string make_error(const std::string& message) const {
        return message + " at byte " + std::to_string(pos_);
    }

    void skip_ws() {
        while (pos_ < text_.size() &&
               std::isspace(static_cast<unsigned char>(text_[pos_]))) {
            ++pos_;
        }
    }

    bool consume(char expected) {
        skip_ws();
        if (pos_ >= text_.size() || text_[pos_] != expected) {
            error_ = make_error(std::string("expected '") + expected + "'");
            return false;
        }
        ++pos_;
        return true;
    }

    bool parse_value(JsonValue& out) {
        skip_ws();
        if (pos_ >= text_.size()) {
            error_ = make_error("unexpected end of input");
            return false;
        }
        char c = text_[pos_];
        if (c == '{') return parse_object(out);
        if (c == '[') return parse_array(out);
        if (c == '"') {
            std::string value;
            if (!parse_string(value)) return false;
            out = JsonValue::make_string(std::move(value));
            return true;
        }
        if (c == 't' || c == 'f') return parse_bool(out);
        if (c == 'n') return parse_null(out);
        if (c == '-' || std::isdigit(static_cast<unsigned char>(c))) {
            return parse_number(out);
        }
        error_ = make_error("unsupported JSON value");
        return false;
    }

    bool parse_object(JsonValue& out) {
        ++pos_;
        JsonObject object;
        skip_ws();
        if (pos_ < text_.size() && text_[pos_] == '}') {
            ++pos_;
            out = JsonValue::make_object(std::move(object));
            return true;
        }
        while (true) {
            skip_ws();
            if (pos_ >= text_.size() || text_[pos_] != '"') {
                error_ = make_error("expected object key");
                return false;
            }
            std::string key;
            if (!parse_string(key)) return false;
            if (!consume(':')) return false;
            JsonValue value;
            if (!parse_value(value)) return false;
            object.emplace_back(std::move(key), std::move(value));
            skip_ws();
            if (pos_ >= text_.size()) {
                error_ = make_error("unterminated object");
                return false;
            }
            if (text_[pos_] == '}') {
                ++pos_;
                break;
            }
            if (text_[pos_] != ',') {
                error_ = make_error("expected comma or closing brace");
                return false;
            }
            ++pos_;
        }
        out = JsonValue::make_object(std::move(object));
        return true;
    }

    bool parse_array(JsonValue& out) {
        ++pos_;
        JsonArray array;
        skip_ws();
        if (pos_ < text_.size() && text_[pos_] == ']') {
            ++pos_;
            out = JsonValue::make_array(std::move(array));
            return true;
        }
        while (true) {
            JsonValue value;
            if (!parse_value(value)) return false;
            array.push_back(std::move(value));
            skip_ws();
            if (pos_ >= text_.size()) {
                error_ = make_error("unterminated array");
                return false;
            }
            if (text_[pos_] == ']') {
                ++pos_;
                break;
            }
            if (text_[pos_] != ',') {
                error_ = make_error("expected comma or closing bracket");
                return false;
            }
            ++pos_;
        }
        out = JsonValue::make_array(std::move(array));
        return true;
    }

    bool parse_string(std::string& out) {
        ++pos_;
        while (pos_ < text_.size()) {
            char c = text_[pos_++];
            if (c == '"') return true;
            if (c == '\\') {
                if (pos_ >= text_.size()) break;
                char escaped = text_[pos_++];
                switch (escaped) {
                    case '"': out.push_back('"'); break;
                    case '\\': out.push_back('\\'); break;
                    case '/': out.push_back('/'); break;
                    case 'b': out.push_back('\b'); break;
                    case 'f': out.push_back('\f'); break;
                    case 'n': out.push_back('\n'); break;
                    case 'r': out.push_back('\r'); break;
                    case 't': out.push_back('\t'); break;
                    default:
                        error_ = make_error("unsupported string escape");
                        return false;
                }
            } else {
                out.push_back(c);
            }
        }
        error_ = make_error("unterminated string");
        return false;
    }

    bool parse_number(JsonValue& out) {
        std::size_t start = pos_;
        if (text_[pos_] == '-') ++pos_;
        while (pos_ < text_.size() &&
               (std::isdigit(static_cast<unsigned char>(text_[pos_])) ||
                text_[pos_] == '.' || text_[pos_] == 'e' ||
                text_[pos_] == 'E' || text_[pos_] == '+' ||
                text_[pos_] == '-')) {
            ++pos_;
        }
        std::string raw(text_.substr(start, pos_ - start));
        out = JsonValue::make_number(std::move(raw));
        return true;
    }

    bool parse_bool(JsonValue& out) {
        if (text_.substr(pos_, 4) == "true") {
            pos_ += 4;
            out = JsonValue::make_bool(true);
            return true;
        }
        if (text_.substr(pos_, 5) == "false") {
            pos_ += 5;
            out = JsonValue::make_bool(false);
            return true;
        }
        error_ = make_error("invalid literal");
        return false;
    }

    bool parse_null(JsonValue& out) {
        if (text_.substr(pos_, 4) != "null") {
            error_ = make_error("invalid literal");
            return false;
        }
        pos_ += 4;
        out = JsonValue::make_null();
        return true;
    }
};

}  // namespace

JsonValue JsonValue::make_null() {
    JsonValue value;
    value.type = Type::Null;
    return value;
}
JsonValue JsonValue::make_bool(bool value) {
    JsonValue result;
    result.type = Type::Boolean;
    result.boolean = value;
    return result;
}
JsonValue JsonValue::make_number(std::string value) {
    JsonValue result;
    result.type = Type::Number;
    result.string_value = std::move(value);
    return result;
}
JsonValue JsonValue::make_string(std::string value) {
    JsonValue result;
    result.type = Type::String;
    result.string_value = std::move(value);
    return result;
}
JsonValue JsonValue::make_array(JsonArray value) {
    JsonValue result;
    result.type = Type::Array;
    result.array = std::move(value);
    return result;
}
JsonValue JsonValue::make_object(JsonObject value) {
    JsonValue result;
    result.type = Type::Object;
    result.object = std::move(value);
    return result;
}

JsonParseResult parse_json(std::string_view text) {
    return Parser(text).parse();
}

const JsonValue* json_find(const JsonValue& value, std::string_view key) {
    if (value.type != JsonValue::Type::Object) return nullptr;
    for (const auto& entry : value.object) {
        if (entry.first == key) return &entry.second;
    }
    return nullptr;
}

std::string json_escape(std::string_view value) {
    std::string result;
    result.reserve(value.size() + 2);
    for (char c : value) {
        switch (c) {
            case '"': result += "\\\""; break;
            case '\\': result += "\\\\"; break;
            case '\n': result += "\\n"; break;
            case '\r': result += "\\r"; break;
            case '\t': result += "\\t"; break;
            default: result.push_back(c); break;
        }
    }
    return result;
}

}  // namespace mbr
