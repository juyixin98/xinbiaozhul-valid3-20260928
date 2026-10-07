cmake_minimum_required(VERSION 3.20)

project(minkowski_service
    VERSION 1.0.0
    DESCRIPTION "Exact convex polygon Minkowski sum and support service"
    LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(Boost 1.74 REQUIRED)

add_library(minkowski_core
    src/config.cpp
    src/convex_set.cpp
    src/convex_hull.cpp
    src/logger.cpp
    src/minkowski.cpp
    src/predicates.cpp
    src/protocol.cpp
    src/support.cpp
    src/types.cpp
    src/verification.cpp)
target_include_directories(minkowski_core PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    ${Boost_INCLUDE_DIRS})
target_compile_features(minkowski_core PUBLIC cxx_std_20)
target_compile_options(minkowski_core PRIVATE
    $<$<CXX_COMPILER_ID:GNU,Clang>:-Wall -Wextra -Wpedantic>)

add_executable(minkowski_service src/main.cpp)
target_link_libraries(minkowski_service PRIVATE minkowski_core)

add_executable(minkowski_fixture_generator tools/generate_fixtures.cpp)
target_link_libraries(minkowski_fixture_generator PRIVATE minkowski_core)

include(CTest)
if(BUILD_TESTING)
    add_library(minkowski_test_support STATIC tests/test_framework.cpp)
    target_include_directories(minkowski_test_support PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/tests
        ${CMAKE_CURRENT_SOURCE_DIR}/include)
    target_link_libraries(minkowski_test_support PUBLIC minkowski_core)

    function(add_minkowski_test test_name)
        add_executable(${test_name} tests/${test_name}.cpp)
        target_link_libraries(${test_name} PRIVATE minkowski_test_support)
        add_test(NAME ${test_name} COMMAND ${test_name})
        set_tests_properties(${test_name} PROPERTIES
            LABELS "geometry;exact-arithmetic")
    endfunction()

    add_minkowski_test(test_predicates)
    add_minkowski_test(test_polygon_validation)
    add_minkowski_test(test_minkowski)
    add_minkowski_test(test_support)
    add_minkowski_test(test_degeneracy)
    add_minkowski_test(test_reference)
    add_minkowski_test(test_protocol)
endif()

install(TARGETS minkowski_service minkowski_fixture_generator
    RUNTIME DESTINATION bin)
