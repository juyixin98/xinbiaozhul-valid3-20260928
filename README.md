# Minimum Area Bounding Rectangle

This project computes a minimum-area axis-oriented bounding rectangle for a
finite set of rational planar points and returns exact contact evidence.
The implementation uses C++20, CMake, and header-only
Boost.Multiprecision (`cpp_int`). It does not use floating point to choose
the optimum or resolve ties.

## Algorithm And Contracts

1. Parse integer, fraction, decimal, and scientific decimal inputs into exact
   rationals.
2. Aggregate duplicate coordinates, retaining every source point id.
3. Build a strict counterclockwise convex hull with the monotonic-chain
   algorithm. One point and fully collinear sets are first-class cases.
4. For every convex hull edge direction, project all unique points onto the
   edge direction and its left normal. A zero-length hull edge is reported
   as `computation_failed`; normal input cannot create one.
5. Select the minimum exact area. Ties use fixed priority:
   smaller perimeter comparator, then the smallest CCW hull edge index.
6. Return all points touching each of the four supporting lines.

The near-collinear predicate is separate from the final error bounds.
`near_collinear_tolerance=t` is evaluated without division as
`cross(a-b,c-b)^2 <= t^2 * |a-b|^2`. A zero-length reference segment does
not divide by zero; it is explicitly marked.

Printed coordinates round half away from zero. Per coordinate the absolute
error is at most `0.5 * 10^-d`. The output includes separate conservative
bounds for reported area rounding, rectangle reconstruction from displayed
corners, and their combined area effect. Internal optimum selection remains
exact regardless of display precision.

## Failure Categories

- `invalid_input`: empty set, malformed JSON, non-finite rational, bad option.
- `state_conflict`: repeated point id in one run.
- `resource_exhausted`: configured point count exceeded or allocation failure.
- `computation_failed`: inconsistent algorithm state, such as zero edge length.

Each result contains the run id, stage, edge index where applicable, event,
and detail, enough to replay the failing JSONL record.

## Build And Example

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
./build/mbr_generate_synthetic | ./build/mbr_batch --decimals 6
./build/mbr_batch --input examples/sample-input.jsonl --decimals 9
```

Shell users can also run `scripts/run_examples.sh`.

Batch input is JSON Lines. A record looks like:

```json
{"run_id":"example","near_collinear_tolerance":"1/1000000","points":[{"id":"a","x":"0","y":"0"},{"id":"b","x":"4","y":"0"},{"id":"c","x":"4","y":"3"},{"id":"d","x":"0","y":"3"}]}
```

Accepted coordinate forms include integers, fractions such as `7/2`, finite
decimals such as `-1.25`, and decimal scientific notation.

## Tests

CTest targets are provided:

- `predicates`: exact orientation and zero-length near-collinear behavior.
- `degenerate_cases`: single point and all-collinear no-division cases.
- `tie_breaks`: concrete square and rectangle edge selection.
- `batch_contracts`: each failure category and replay diagnostics.
- `exhaustive_projection`: an independent oracle enumerates all point-pair
  directions, checks optimum and containment, and independently checks contact
  ids.

Build and run in a normal development environment with:

```sh
cmake -S . -B build
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

## Limitations

The input parser is intentionally local and small; it supports JSON used by
the documented batch contract, not arbitrary Unicode JSON escape sequences.
Very large rational inputs can be expensive because exact integer growth is
unavoidable under the stated precision contract. The side length and perimeter
fields use conservative exact upper bounds for otherwise irrational square
roots; area, comparisons, corners, and contact decisions remain exact.
