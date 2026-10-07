#pragma once

#include "minkowski/types.h"

#include <functional>
#include <iosfwd>
#include <cstddef>
#include <sstream>
#include <string>
#include <vector>

namespace minkowski::test {

struct Case {
    std::string name;
    std::function<void()> body;
};

class Registry {
public:
    static Registry& instance();
    void add(std::string name, std::function<void()> body);
    int run_all();

private:
    std::vector<Case> cases_;
};

struct Registrar {
    Registrar(const char* name, std::function<void()> body);
};

void check_true(bool condition, const std::string& expression,
                const char* file, int line);
void check_equal_strings(const std::string& actual,
                         const std::string& expected,
                         const std::string& actual_expression,
                         const char* file, int line);

std::string points_to_string(const std::vector<Point>& points);

template <typename Actual, typename Expected>
void check_equal(const Actual& actual, const Expected& expected,
                 const char* actual_expression,
                 const char* expected_expression,
                 const char* file, int line) {
    std::ostringstream a;
    std::ostringstream e;
    a << actual;
    e << expected;
    check_equal_strings(a.str(), e.str(),
                        std::string(actual_expression) + " == " +
                        expected_expression,
                        file, line);
}

} // namespace minkowski::test

#define TEST_CASE_CONCAT_IMPL(left, right) left##right
#define TEST_CASE_CONCAT(left, right) TEST_CASE_CONCAT_IMPL(left, right)

#define TEST_CASE(name)                                                      \
    namespace TEST_CASE_CONCAT(test_case_, __LINE__) { void run(); }          \
    namespace {                                                               \
    ::minkowski::test::Registrar TEST_CASE_CONCAT(registrar_, __LINE__)       \
        (name, [] { TEST_CASE_CONCAT(test_case_, __LINE__)::run(); });        \
    }                                                                         \
    void TEST_CASE_CONCAT(test_case_, __LINE__)::run()                        \

#define CHECK(expression)                                                    \
    ::minkowski::test::check_true(static_cast<bool>(expression), #expression,\
                                  __FILE__, __LINE__)

#define CHECK_EQ(actual, expected)                                           \
    ::minkowski::test::check_equal(actual, expected, #actual, #expected,     \
                                   __FILE__, __LINE__)

#define CHECK_THROWS(expression)                                             \
    do {                                                                     \
        bool threw = false;                                                  \
        try {                                                                \
            expression;                                                      \
        } catch (const std::exception&) {                                    \
            threw = true;                                                    \
        }                                                                    \
        ::minkowski::test::check_true(threw,                                 \
                                      "throws: " #expression,                \
                                      __FILE__, __LINE__);                   \
    } while (false)
