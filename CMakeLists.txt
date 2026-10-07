cmake_minimum_required(VERSION 3.20)

project(minimum_enclosing_rectangle
  VERSION 1.0.0
  DESCRIPTION "Exact convex hull minimum-area enclosing rectangle with contact evidence"
  LANGUAGES CXX)

# Lock the project language level. Dependencies are restricted to the standard
# library and Boost.Multiprecision, which is header-only for this workload.
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(Boost 1.74 REQUIRED)

add_library(mer
  src/types.cpp
  src/errors.cpp
  src/predicates.cpp
  src/hull.cpp
  src/algorithm.cpp
  src/json.cpp
  src/batch.cpp)

target_include_directories(mer
  PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
  PRIVATE
    "${CMAKE_CURRENT_SOURCE_DIR}/src")

target_link_libraries(mer PUBLIC Boost::boost)
target_compile_options(mer PRIVATE
  $<$<CXX_COMPILER_ID:GNU,Clang>:-Wall -Wextra -Wpedantic>)

add_executable(mer_batch apps/mer_batch.cpp)
target_link_libraries(mer_batch PRIVATE mer)

add_executable(mer_generate_fixture tools/generate_fixture.cpp)
target_link_libraries(mer_generate_fixture PRIVATE mer)

enable_testing()
add_executable(test_reference tests/test_reference.cpp)
target_link_libraries(test_reference PRIVATE mer)
add_test(NAME independent_reference_checks COMMAND test_reference)

install(TARGETS mer mer_batch mer_generate_fixture
  RUNTIME DESTINATION bin
  ARCHIVE DESTINATION lib
  LIBRARY DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
