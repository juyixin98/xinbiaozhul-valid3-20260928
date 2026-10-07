# Data And Error Contract

## Input Record

`run_id` is an optional string; JSONL line numbers are used as fallback ids.
`points` is required and non-empty. Each point has required rational `x` and
`y` and an optional string `id`; omitted ids become `p0`, `p1`, and so on.

Options may appear per record:

- `max_input_points`: non-negative integer record limit.
- `near_collinear_tolerance`: non-negative rational distance threshold.
- `output_decimals`: integer from 0 through 1000.

## Success Result

The successful JSON line contains counts, degeneration kind, selected hull
edge index, exact edge vector, displayed corners, exact area and side
expressions, complete hull vertices, one evaluation per hull edge direction,
contact ids, near-collinear observations, and error bounds.

`degenerate_kind` is one of:

- `single_point`: all coordinates coincide.
- `collinear`: all unique coordinates lie on one line.
- `none`: hull has at least three vertices.

## Contact Names

- `tangent_min`: points with minimum projection on the selected edge.
- `tangent_max`: points with maximum projection on the selected edge.
- `normal_min`: points with minimum projection on the left normal.
- `normal_max`: points with maximum projection on the left normal.

Collinear boundary points with different ids are included. Interior points
are not contact evidence.

## Diagnostics

Every diagnostic carries:

- `run_id`: stable replay key.
- `stage`: such as `json_parse`, `record_parse`, or `solve`.
- `edge_index`: zero-based hull edge index, or `-1` when not applicable.
- `event`: short machine-readable event name.
- `detail`: human-readable reason.

Errors are never collapsed into a boolean. The four application failure
classes are distinct from successful degenerate geometry.
