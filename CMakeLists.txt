cmake_minimum_required(VERSION 3.20)
project(mileage_service LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

# Boost.Multiprecision is header-only; locate the headers explicitly so the
# project also configures on systems without Boost CMake config packages.
find_path(BOOST_INCLUDE_DIR boost/multiprecision/cpp_int.hpp)
if(NOT BOOST_INCLUDE_DIR)
  message(FATAL_ERROR "Boost.Multiprecision headers not found. Install libboost-dev (>= 1.74).")
endif()
message(STATUS "Using Boost headers from: ${BOOST_INCLUDE_DIR}")

add_library(mileage_core STATIC
  src/geometry/types.cpp
  src/geometry/predicates.cpp
  src/geometry/polyline.cpp
  src/geometry/projection.cpp
  src/spatial/brute_force.cpp
  src/spatial/bvh_index.cpp
  src/spatial/candidate_set.cpp
  src/validate/degenerate.cpp
  src/batch/batch_runner.cpp
  src/io/formats.cpp
)
target_include_directories(mileage_core PUBLIC src ${BOOST_INCLUDE_DIR})
target_compile_options(mileage_core PRIVATE -Wall -Wextra)

add_executable(mileage_query apps/mileage_query_main.cpp)
target_link_libraries(mileage_query PRIVATE mileage_core)

add_executable(mileage_batch apps/mileage_batch_main.cpp)
target_link_libraries(mileage_batch PRIVATE mileage_core)

add_executable(mileage_validate apps/mileage_validate_main.cpp)
target_link_libraries(mileage_validate PRIVATE mileage_core)

add_executable(gen_fixtures apps/gen_fixtures_main.cpp)
target_link_libraries(gen_fixtures PRIVATE mileage_core)

enable_testing()
add_subdirectory(tests)
