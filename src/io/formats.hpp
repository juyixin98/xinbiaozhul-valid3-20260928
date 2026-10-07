#pragma once

#include <iosfwd>
#include <string>
#include <vector>

#include "batch/batch_runner.hpp"
#include "geometry/polyline.hpp"

namespace mileage {

// Line-based local fixture formats (no external dependencies):
//
// Polyline file: one vertex per line, "x y" as decimal integers; blank lines
// and lines starting with '#' are ignored.
//
// Query file: one request per line, "Q <id> <x> <y> [label]"; '#' comments.
//
// Result file: one record per line,
// "R <id> <STATUS> reason=<r> candidates=<k> seg=<i> t=<n/d> dist2=<n/d> mileage=<m>"

struct ParseError {
  size_t line_no = 0;
  std::string message;
};

struct PolylineFile {
  Polyline line;
  std::vector<ParseError> errors;  // non-fatal: bad lines are skipped
};

PolylineFile read_polyline(std::istream& in);

struct QueryFile {
  std::vector<BatchQuery> queries;
  std::vector<ParseError> errors;
};

QueryFile read_queries(std::istream& in);

void write_result(std::ostream& out, const BatchOutcome& o);

}  // namespace mileage
