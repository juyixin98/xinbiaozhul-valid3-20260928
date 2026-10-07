#pragma once

#include <iosfwd>
#include <map>
#include <string>

namespace minkowski {

enum class LogLevel {
    Quiet,
    Error,
    Info,
    Debug
};

struct ServiceConfig {
    LogLevel log_level = LogLevel::Info;
    bool log_to_stderr = true;
    bool continue_on_error = true;
    bool emit_step_details = true;

    static ServiceConfig from_file(const std::string& path);
    static ServiceConfig from_map(const std::map<std::string, std::string>&);
    void write(std::ostream& out) const;
};

LogLevel parse_log_level(const std::string& text);
std::string to_string(LogLevel level);

} // namespace minkowski
