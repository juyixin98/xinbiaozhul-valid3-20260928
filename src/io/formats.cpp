#include "io/formats.hpp"

#include <iomanip>
#include <istream>
#include <ostream>
#include <sstream>

namespace mileage {

namespace {

bool parse_int(const std::string& s, cpp_int& out) {
  if (s.empty()) return false;
  size_t i = (s[0] == '-' || s[0] == '+') ? 1 : 0;
  if (i == s.size()) return false;
  for (; i < s.size(); ++i) {
    if (s[i] < '0' || s[i] > '9') return false;
  }
  try {
    out = cpp_int(s);
  } catch (...) {
    return false;
  }
  return true;
}

bool comment_or_blank(const std::string& line) {
  const auto pos = line.find_first_not_of(" \t\r");
  return pos == std::string::npos || line[pos] == '#';
}

}  // namespace

PolylineFile read_polyline(std::istream& in) {
  PolylineFile pf;
  std::vector<Vec2> pts;
  std::string line;
  size_t line_no = 0;
  while (std::getline(in, line)) {
    ++line_no;
    if (comment_or_blank(line)) continue;
    std::istringstream is(line);
    std::string xs, ys, extra;
    if (!(is >> xs >> ys) || (is >> extra)) {
      pf.errors.push_back({line_no, "expected 'x y'"});
      continue;
    }
    Vec2 p;
    if (!parse_int(xs, p.x) || !parse_int(ys, p.y)) {
      pf.errors.push_back({line_no, "non-integer coordinate"});
      continue;
    }
    pts.push_back(std::move(p));
  }
  pf.line = Polyline(std::move(pts));
  return pf;
}

QueryFile read_queries(std::istream& in) {
  QueryFile qf;
  std::string line;
  size_t line_no = 0;
  while (std::getline(in, line)) {
    ++line_no;
    if (comment_or_blank(line)) continue;
    std::istringstream is(line);
    std::string tag;
    if (!(is >> tag) || tag != "Q") {
      qf.errors.push_back({line_no, "expected 'Q <id> <x> <y> [label]'"});
      continue;
    }
    BatchQuery q;
    std::string xs, ys;
    if (!(is >> q.id >> xs >> ys)) {
      qf.errors.push_back({line_no, "missing id or coordinates"});
      continue;
    }
    if (!parse_int(xs, q.point.x) || !parse_int(ys, q.point.y)) {
      qf.errors.push_back({line_no, "non-integer coordinate"});
      continue;
    }
    std::getline(is, q.label);
    const auto pos = q.label.find_first_not_of(" \t");
    q.label = pos == std::string::npos ? "" : q.label.substr(pos);
    qf.queries.push_back(std::move(q));
  }
  return qf;
}

void write_result(std::ostream& out, const BatchOutcome& o) {
  out << "R " << (o.query.id.empty() ? "-" : o.query.id) << ' '
      << to_string(o.result.status) << " reason=" << o.result.reason
      << " candidates=" << o.result.candidates.size();
  for (const auto& c : o.result.candidates) {
    out << " | seg=" << c.seg_index << " t=" << to_string(c.t)
        << " dist2=" << to_string(c.dist2) << " mileage=" << std::fixed
        << std::setprecision(6) << static_cast<double>(c.mileage);
  }
  out << '\n';
}

}  // namespace mileage
