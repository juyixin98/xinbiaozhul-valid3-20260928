#pragma once

#include <iostream>
#include <string>

#define CHECK(...)                                                            \
    do {                                                                      \
        if (!(__VA_ARGS__)) {                                                 \
            std::cerr << "CHECK failed at " << __FILE__ << ':' << __LINE__    \
                      << ": " #__VA_ARGS__ << '\n';                           \
            return 1;                                                         \
        }                                                                     \
    } while (false)

#define CHECK_EQ(actual, expected)                                            \
    do {                                                                      \
        auto actual_value = (actual);                                         \
        auto expected_value = (expected);                                     \
        if (!(actual_value == expected_value)) {                              \
            std::cerr << "CHECK_EQ failed at " << __FILE__ << ':'             \
                      << __LINE__ << "\n  actual: " << actual_value           \
                      << "\n  expected: " << expected_value << '\n';          \
            return 1;                                                         \
        }                                                                     \
    } while (false)
