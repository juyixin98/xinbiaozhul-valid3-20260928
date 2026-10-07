set(MILEAGE_TESTS
  test_predicates
  test_projection
  test_index_vs_scan
  test_degenerate
  test_batch
)

foreach(t IN LISTS MILEAGE_TESTS)
  add_executable(${t} ${t}.cpp)
  target_link_libraries(${t} PRIVATE mileage_core)
  target_include_directories(${t} PRIVATE ${CMAKE_CURRENT_SOURCE_DIR})
  add_test(NAME ${t} COMMAND ${t})
endforeach()
