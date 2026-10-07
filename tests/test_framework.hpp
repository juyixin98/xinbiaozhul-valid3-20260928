#pragma once

#include <functional>
#include <iostream>
#include <string>
#include <vector>

namespace testlib {

struct TestCase {
    std::string name;
    std::function<void()> body;
};

inline std::vector<TestCase>& registry() {
    static std::vector<TestCase> tests;
    return tests;
}

struct Registrar {
    Registrar(const std::string& name, std::function<void()> body) {
        registry().push_back({name, std::move(body)});
    }
};

inline void check(bool condition, const std::string& message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

}  // namespace testlib

#define TEST_CASE(name)                                                      \
    static void name();                                                      \
    static testlib::Registrar registrar_##name(#name, name);                 \
    static void name()
