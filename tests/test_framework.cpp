#include "test_framework.h"

#include <exception>
#include <iostream>
#include <sstream>

namespace minkowski::test {

Registry& Registry::instance() {
    static Registry registry;
    return registry;
}

void Registry::add(std::string name, std::function<void()> body) {
    cases_.push_back({std::move(name), std::move(body)});
}

int Registry::run_all() {
    const std::string run_id = std::string("ctest-local-") +
                               std::to_string(1 + static_cast<int>(cases_.size()));
    std::cout << "[run] id=" << run_id
              << " service-version=1.0.0"
              << " cases=" << cases_.size() << '\n';
    int failures = 0;
    for (std::size_t i = 0; i < cases_.size(); ++i) {
        std::cout << "[step " << (i + 1) << "/" << cases_.size()
                  << "] input=" << cases_[i].name << '\n';
        try {
            cases_[i].body();
            std::cout << "[pass] input=" << cases_[i].name << '\n';
        } catch (const std::exception& ex) {
            ++failures;
            std::cout << "[fail] input=" << cases_[i].name
                      << " category=ASSERTION_FAILURE evidence="
                      << ex.what() << '\n';
        } catch (...) {
            ++failures;
            std::cout << "[fail] input=" << cases_[i].name
                      << " category=UNKNOWN_EXCEPTION\n";
        }
    }
    std::cout << "[summary] passed=" << (cases_.size() - failures)
              << " failed=" << failures << '\n';
    return failures == 0 ? 0 : 1;
}

Registrar::Registrar(const char* name, std::function<void()> body) {
    Registry::instance().add(name, std::move(body));
}

void check_true(bool condition, const std::string& expression,
                const char* file, int line) {
    if (!condition) {
        std::ostringstream message;
        message << file << ":" << line << " expected true: " << expression;
        throw std::runtime_error(message.str());
    }
}

void check_equal_strings(const std::string& actual,
                         const std::string& expected,
                         const std::string& actual_expression,
                         const char* file, int line) {
    if (actual != expected) {
        std::ostringstream message;
        message << file << ":" << line
                << " " << actual_expression
                << " actual='" << actual
                << "' expected='" << expected << "'";
        throw std::runtime_error(message.str());
    }
}

std::string points_to_string(const std::vector<Point>& points) {
    std::ostringstream out;
    for (std::size_t i = 0; i < points.size(); ++i) {
        if (i) out << ";";
        out << points[i].x.str() << "," << points[i].y.str();
    }
    return out.str();
}

} // namespace minkowski::test

int main() {
    return ::minkowski::test::Registry::instance().run_all();
}
