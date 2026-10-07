#include "minkowski/config.h"

#include <algorithm>
#include <cctype>
#include <fstream>
#include <sstream>
#include <stdexcept>

namespace minkowski {

namespace {

std::string trim(std::string text) {
    const auto is_space = [](unsigned char c) { return std::isspace(c); };
    text.erase(text.begin(),
               std::find_if_not(text.begin(), text.end(), is_space));
    text.erase(std::find_if_not(text.rbegin(), text.rend(), is_space).base(),
               text.end());
    return text;
}

bool parse_bool(const std::string& text) {
    std::string value = text;
    std::transform(value.begin(), value.end(), value.begin(),
                   [](unsigned char c) {
                       return static_cast<char>(std::tolower(c));
                   });
    if (value == "true" || value == "1" || value == "yes" || value == "on") {
        return true;
    }
    if (value == "false" || value == "0" || value == "no" ||
        value == "off") {
        return false;
    }
    throw std::runtime_error("invalid boolean configuration value: " + text);
}

} // namespace

LogLevel parse_log_level(const std::string& text) {
    if (text == "quiet") return LogLevel::Quiet;
    if (text == "error") return LogLevel::Error;
    if (text == "info") return LogLevel::Info;
    if (text == "debug") return LogLevel::Debug;
    throw std::runtime_error("invalid log level: " + text);
}

std::string to_string(LogLevel level) {
    switch (level) {
    case LogLevel::Quiet:
        return "quiet";
    case LogLevel::Error:
        return "error";
    case LogLevel::Info:
        return "info";
    case LogLevel::Debug:
        return "debug";
    }
    return "unknown";
}

ServiceConfig ServiceConfig::from_map(
    const std::map<std::string, std::string>& values) {
    ServiceConfig config;
    if (const auto it = values.find("log-level"); it != values.end()) {
        config.log_level = parse_log_level(it->second);
    }
    if (const auto it = values.find("log-stream"); it != values.end()) {
        config.log_to_stderr = (it->second == "stderr");
    }
    if (const auto it = values.find("continue-on-error"); it != values.end()) {
        config.continue_on_error = parse_bool(it->second);
    }
    if (const auto it = values.find("emit-step-details"); it != values.end()) {
        config.emit_step_details = parse_bool(it->second);
    }
    return config;
}

ServiceConfig ServiceConfig::from_file(const std::string& path) {
    std::ifstream file(path);
    if (!file) throw std::runtime_error("cannot open config: " + path);

    std::map<std::string, std::string> values;
    std::string line;
    while (std::getline(file, line)) {
        const std::string cleaned = trim(line);
        if (cleaned.empty() || cleaned.starts_with("#")) continue;
        const auto pos = cleaned.find('=');
        if (pos == std::string::npos) {
            throw std::runtime_error("invalid config line: " + line);
        }
        values.emplace(trim(cleaned.substr(0, pos)),
                       trim(cleaned.substr(pos + 1)));
    }
    return from_map(values);
}

void ServiceConfig::write(std::ostream& out) const {
    out << "log-level=" << to_string(log_level)
        << " log-stream=" << (log_to_stderr ? "stderr" : "stdout")
        << " continue-on-error=" << (continue_on_error ? "true" : "false")
        << " emit-step-details=" << (emit_step_details ? "true" : "false");
}

} // namespace minkowski
