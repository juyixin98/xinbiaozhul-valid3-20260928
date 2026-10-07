cmake_minimum_required(VERSION 3.20)

project(nearest_polyline_projection
    VERSION 1.0.0
    DESCRIPTION "Exact nearest projection and mileage localization on large polylines"
    LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(Boost 1.74 REQUIRED)

add_library(nearest_projection_core
    src/geometry/decimal.cpp
    src/geometry/predicates.cpp
    src/geometry/polyline.cpp
    src/geometry/reference_scan.cpp
    src/spatial/bvh_index.cpp
    src/service/configuration.cpp
    src/service/diagnostics.cpp
    src/service/polyline_projector.cpp
    src/service/nearest_projection_service.cpp
    src/io/batch_io.cpp
)

target_include_directories(nearest_projection_core
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
        ${Boost_INCLUDE_DIRS}
)

target_compile_options(nearest_projection_core
    PRIVATE
        $<$<CXX_COMPILER_ID:GNU,Clang>:-Wall;-Wextra;-Wpedantic>
)

add_executable(nearest_projection_batch
    src/batch/main.cpp
)
target_link_libraries(nearest_projection_batch
    PRIVATE nearest_projection_core
)

add_executable(generate_fixtures
    src/tools/generate_fixtures.cpp
)
target_link_libraries(generate_fixtures
    PRIVATE nearest_projection_core
)

enable_testing()

add_executable(unit_tests
    tests/test_main.cpp
    tests/test_predicates.cpp
    tests/test_projection_cases.cpp
    tests/test_bvh_agreement.cpp
)
target_link_libraries(unit_tests
    PRIVATE nearest_projection_core
)

add_test(
    NAME nearest_projection_tests
    COMMAND unit_tests
)

install(TARGETS nearest_projection_batch generate_fixtures
    RUNTIME DESTINATION bin
)
