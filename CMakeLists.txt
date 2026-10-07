cmake_minimum_required(VERSION 3.20)
project(minkowski_service VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Boost.Multiprecision is header-only; we only need the include path.
find_package(Boost 1.74 REQUIRED)
message(STATUS "Boost include dirs: ${Boost_INCLUDE_DIRS}")

add_library(geom INTERFACE)
target_include_directories(geom INTERFACE
  ${CMAKE_CURRENT_SOURCE_DIR}/include
  ${Boost_INCLUDE_DIRS})
if(TARGET Boost::headers)
  target_link_libraries(geom INTERFACE Boost::headers)
endif()

add_executable(minkcli src/main.cpp)
target_link_libraries(minkcli PRIVATE geom)
target_compile_options(minkcli PRIVATE -Wall -Wextra)

add_executable(mink_tests tests/test_main.cpp)
target_link_libraries(mink_tests PRIVATE geom)
target_include_directories(mink_tests PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/tests)
target_compile_options(mink_tests PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME mink_tests COMMAND mink_tests --run-id ctest)
