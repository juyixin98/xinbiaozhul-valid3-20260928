#include "minkowski/logger.h"

#include <chrono>
#include <ctime>
#include <iomanip>
#include <sstream>
#include <utility>

namespace minkowski {

ProgressLogger::ProgressLogger(ServiceConfig config, std::ostream& out,
                               std::ostream& err)
    : config_(std::move(config)), out_(out), err_(err),
      start_(std::chrono::system_clock::now()) {}

void ProgressLogger::set_run_id(std::string id) {
    run_id_ = std::move(id);
}

bool ProgressLogger::enabled(LogLevel required) const {
    return config_.log_level >= required;
}

std::string ProgressLogger::timestamp() const {
    const auto now = std::chrono::system_clock::now();
    const std::time_t t = std::chrono::system_clock::to_time_t(now);
    std::tm tm{};
#if defined(_WIN32)
    localtime_s(&tm, &t);
#else
    localtime_r(&t, &tm);
#endif
    std::ostringstream s;
    s << std::put_time(&tm, "%Y-%m-%dT%H:%M:%S%z");
    return s.str();
}

void ProgressLogger::write(LogLevel level, const std::string& tag,
                           const std::string& message) {
    if (!enabled(level)) return;
    auto& target = config_.log_to_stderr ? err_ : out_;
    target << timestamp() << " run=" << run_id_
           << " step=" << step_
           << " level=" << to_string(level)
           << " " << tag << " " << message << '\n';
}

void ProgressLogger::command(const std::string& line) {
    ++step_;
    write(LogLevel::Info, "command", "input=" + line);
}

void ProgressLogger::detail(const std::string& message) {
    if (config_.emit_step_details) write(LogLevel::Debug, "step", message);
}

void ProgressLogger::info(const std::string& message) {
    write(LogLevel::Info, "info", message);
}

void ProgressLogger::error(const std::string& category,
                           const std::string& message) {
    write(LogLevel::Error, "error",
          "category=" + category + " reason=" + message);
}

void ProgressLogger::result(const std::string& message) {
    write(LogLevel::Info, "result", message);
}

} // namespace minkowski
