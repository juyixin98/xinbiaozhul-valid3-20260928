cmake_minimum_required(VERSION 3.20)

project(minimum_bounding_rectangle
    VERSION 1.0.0
    LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(Boost 1.83.0 EXACT REQUIRED)

add_library(mbr_core
    src/rational.cpp
    src/geometry.cpp
    src/diagnostics.cpp
    src/json.cpp
    src/predicates.cpp
    src/convex_hull.cpp
    src/enclosing_rectangle.cpp
    src/solver.cpp
    src/batch.cpp
    src/enclosing_rectangle.cpp)
target_include_directories(mbr_core PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>")
target_link_libraries(mbr_core PUBLIC Boost::boost)
target_compile_options(mbr_core PRIVATE
    $<$<CXX_COMPILER_ID:GNU,Clang>:-Wall -Wextra -Wpedantic>)

add_executable(mbr_batch src/cli.cpp)
target_link_libraries(mbr_batch PRIVATE mbr_core)

add_executable(mbr_generate_synthetic src/generator.cpp)
target_link_libraries(mbr_generate_synthetic PRIVATE mbr_core)

enable_testing()
add_executable(test_predicates tests/test_predicates.cpp)
target_link_libraries(test_predicates PRIVATE mbr_core)
add_test(NAME predicates COMMAND test_predicates)

add_executable(test_degenerate_cases tests/test_degenerate_cases.cpp)
target_link_libraries(test_degenerate_cases PRIVATE mbr_core)
add_test(NAME degenerate_cases COMMAND test_degenerate_cases)

add_executable(test_tie_breaks tests/test_tie_breaks.cpp)
target_link_libraries(test_tie_breaks PRIVATE mbr_core)
add_test(NAME tie_breaks COMMAND test_tie_breaks)

add_executable(test_batch_contracts tests/test_batch_contracts.cpp)
target_link_libraries(test_batch_contracts PRIVATE mbr_core)
add_test(NAME batch_contracts COMMAND test_batch_contracts)

add_executable(test_exhaustive_projection tests/test_exhaustive_projection.cpp)
target_link_libraries(test_exhaustive_projection PRIVATE mbr_core)
add_test(NAME exhaustive_projection COMMAND test_exhaustive_projection)
