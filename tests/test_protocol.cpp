#include "test_framework.h"

#include "minkowski/protocol.h"

#include <sstream>

using namespace minkowski;

namespace {

struct ServiceResult {
    int code;
    std::string output;
};

ServiceResult run_service(const std::string& input) {
    ServiceConfig config;
    config.log_level = LogLevel::Quiet;
    config.emit_step_details = false;
    std::istringstream in(input);
    std::ostringstream out;
    std::ostringstream err;
    BatchService service(config, in, out, err);
    return {service.run(), out.str()};
}

} // namespace

TEST_CASE("protocol:happy path sum and explicit verification") {
    const auto result = run_service(
        "RUN protocol-case-1\n"
        "POLY A 0 0 1 0 1 1 0 1\n"
        "POLY B 2 3 3 3 3 4 2 4\n"
        "SUM C A B VERIFY\n"
        "SUPPORT C 1 1\n");
    CHECK_EQ(result.code, 0);
    CHECK(result.output.find("OK SUM name=C polygon 4 2 3 4 3 4 5 2 5")
          != std::string::npos);
    CHECK(result.output.find("VERIFY PASS") != std::string::npos);
    CHECK(result.output.find("OK SUPPORT set=C direction=1 1 value=9")
          != std::string::npos);
    CHECK(result.output.find("DONE failures=0") != std::string::npos);
}

TEST_CASE("protocol:nonconvex input is an error category, not success") {
    const auto result = run_service(
        "POLY BAD 0 0 3 0 3 3 2 1 0 3\nSHOW BAD\n");
    CHECK_EQ(result.code, 1);
    CHECK(result.output.find("ERROR category=COMMAND_FAILED reason=NON_CONVEX")
          != std::string::npos);
    CHECK(result.output.find("OK POLY") == std::string::npos);
    CHECK(result.output.find("DONE failures=1") != std::string::npos);
}

TEST_CASE("protocol:bad integer is reported explicitly") {
    const auto result = run_service("POLY A 0 x 1 0\n");
    CHECK_EQ(result.code, 1);
    CHECK(result.output.find("reason=not an exact integer: x")
          != std::string::npos);
}

TEST_CASE("protocol:unknown set does not silently succeed") {
    const auto result = run_service("SUM C A B\n");
    CHECK_EQ(result.code, 1);
    CHECK(result.output.find("unknown SUM operand") != std::string::npos);
}
