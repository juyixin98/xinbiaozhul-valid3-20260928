#pragma once

#include <map>
#include <memory>
#include <string>
#include <vector>

namespace mbr {

struct JsonValue;
using JsonObject = std::vector<std::pair<std::string, JsonValue>>;
using JsonArray = std::vector<JsonValue>;

struct JsonValue {
    enum class Type { Null, Boolean, Number, String, Array, Object };
    Type type = Type::Null;
    bool boolean = false;
    std::string string_value;
    JsonArray array;
    JsonObject object;

    static JsonValue make_null();
    static JsonValue make_bool(bool value);
    static JsonValue make_number(std::string value);
    static JsonValue make_string(std::string value);
    static JsonValue make_array(JsonArray value = {});
    static JsonValue make_object(JsonObject value = {});
};

struct JsonParseResult {
    bool ok = false;
    std::string error;
    JsonValue value;
};

JsonParseResult parse_json(std::string_view text);
const JsonValue* json_find(const JsonValue& value, std::string_view key);
std::string json_escape(std::string_view value);

}  // namespace mbr
