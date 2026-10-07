#pragma once

#include "minkowski/config.h"

#include <chrono>
#include <iosfwd>
#include <string>

namespace minkowski {

class ProgressLogger {
public:
    ProgressLogger(ServiceConfig config, std::ostream& out, std::ostream& err);

    void set_run_id(std::string id);
    const std::string& run_id() const noexcept { return run_id_; }
    std::size_t step() const noexcept { return step_; }

    void command(const std::string& line);
    void detail(const std::string& message);
    void info(const std::string& message);
    void error(const std::string& category, const std::string& message);
    void result(const std::string& message);

private:
    bool enabled(LogLevel required) const;
    std::string timestamp() const;
    void write(LogLevel level, const std::string& tag,
               const std::string& message);

    ServiceConfig config_;
    std::ostream& out_;
    std::ostream& err_;
    std::string run_id_ = "local-run";
    std::size_t step_ = 0;
    std::chrono::system_clock::time_point start_;
};

} // namespace minkowski
