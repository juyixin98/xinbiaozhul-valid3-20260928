#include "test_framework.hpp"

#include <exception>

int main() {
    int failed = 0;
    for (const testlib::TestCase& test : testlib::registry()) {
        try {
            test.body();
            std::cout << "[PASS] " << test.name << '\n';
        } catch (const std::exception& error) {
            ++failed;
            std::cout << "[FAIL] " << test.name << ": " << error.what() << '\n';
        }
    }
    std::cout << (testlib::registry().size() - static_cast<std::size_t>(failed))
              << " passed, " << failed << " failed\n";
    return failed == 0 ? 0 : 1;
}
