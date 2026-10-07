// minkcli: batch entry point for convex Minkowski sums and support queries.
//
// Job file format (token based, '#' starts a comment):
//   polygon <name> <n>        followed by n pairs of coordinates "x y"
//                             (each coordinate is an integer or "p/q")
//   sum <A> <B>               print the Minkowski sum boundary of A (+) B
//   support <name> <dx> <dy>  print support value/point of a stored shape
//   additivity <A> <B> <dx> <dy>
//                             check h_{A+B}(d) == h_A(d) + h_B(d)
//
// Exit codes: 0 ok, 2 usage/parse error, 3 geometry error (non-convex
// input etc.), 4 additivity mismatch.

#include <fstream>
#include <iostream>
#include <map>
#include <sstream>
#include <string>
#include <vector>

#include "algo/minkowski.hpp"
#include "algo/support.hpp"
#include "geom/polygon.hpp"
#include "version.hpp"

namespace {

void log(const std::string& msg) {
  std::cerr << "[minkcli " << geom::kVersion << "] " << msg << '\n';
}

struct ParseError : std::runtime_error {
  using std::runtime_error::runtime_error;
};

class Job {
 public:
  explicit Job(std::istream& in) {
    // Strip comments, then tokenize the whole stream.
    std::string text, line;
    while (std::getline(in, line)) {
      const auto hash = line.find('#');
      text += line.substr(0, hash);
      text += '\n';
    }
    tokens_.str(text);
  }

  int run() {
    std::string cmd;
    while (tokens_ >> cmd) {
      if (cmd == "polygon") {
        cmd_polygon();
      } else if (cmd == "sum") {
        cmd_sum();
      } else if (cmd == "support") {
        cmd_support();
      } else if (cmd == "additivity") {
        if (!cmd_additivity()) return 4;
      } else {
        throw ParseError("unknown command: " + cmd);
      }
    }
    return 0;
  }

 private:
  std::string next(const std::string& what) {
    std::string t;
    if (!(tokens_ >> t)) throw ParseError("unexpected end of job, want " + what);
    return t;
  }

  const geom::ConvexPolygon& shape(const std::string& name) {
    const auto it = shapes_.find(name);
    if (it == shapes_.end()) throw ParseError("unknown polygon: " + name);
    return it->second;
  }

  void cmd_polygon() {
    const std::string name = next("polygon name");
    const int n = std::stoi(next("vertex count"));
    if (n < 1) throw ParseError("polygon needs >= 1 vertex");
    std::vector<geom::Point> pts;
    pts.reserve(n);
    for (int i = 0; i < n; ++i) {
      pts.push_back({geom::parse_rational(next("x")),
                     geom::parse_rational(next("y"))});
    }
    auto poly = geom::ConvexPolygon::from_points(std::move(pts));
    log("polygon " + name + ": kind=" + geom::to_string(poly.kind()) +
        " vertices=" + std::to_string(poly.size()));
    shapes_.emplace(name, std::move(poly));
  }

  void cmd_sum() {
    const auto& a = shape(next("operand A"));
    const auto& b = shape(next("operand B"));
    const auto s = geom::minkowski_sum(a, b);
    log("sum: kind=" + std::string(geom::to_string(s.kind())) +
        " vertices=" + std::to_string(s.size()));
    std::cout << "SUM kind=" << geom::to_string(s.kind())
              << " vertices=" << s.size() << '\n';
    for (const auto& p : s.vertices())
      std::cout << geom::to_string(p.x) << ' ' << geom::to_string(p.y) << '\n';
  }

  void cmd_support() {
    const auto& p = shape(next("shape name"));
    const geom::Point d{geom::parse_rational(next("dx")),
                        geom::parse_rational(next("dy"))};
    const auto v = geom::support_value(p, d);
    const auto sp = geom::support_point(p, d);
    log("support " + geom::to_string(d) + " -> " + geom::to_string(v));
    std::cout << "SUPPORT value=" << geom::to_string(v)
              << " point=" << geom::to_string(sp) << '\n';
  }

  bool cmd_additivity() {
    const auto& a = shape(next("operand A"));
    const auto& b = shape(next("operand B"));
    const geom::Point d{geom::parse_rational(next("dx")),
                        geom::parse_rational(next("dy"))};
    const auto sum = geom::minkowski_sum(a, b);
    const auto ha = geom::support_value(a, d);
    const auto hb = geom::support_value(b, d);
    const auto hs = geom::support_value(sum, d);
    const bool ok = (hs == ha + hb);
    log(std::string("additivity d=") + geom::to_string(d) +
        (ok ? " OK" : " MISMATCH"));
    std::cout << "ADDITIVITY h_A=" << geom::to_string(ha)
              << " h_B=" << geom::to_string(hb)
              << " h_sum=" << geom::to_string(hs)
              << " status=" << (ok ? "OK" : "MISMATCH") << '\n';
    return ok;
  }

  std::istringstream tokens_;
  std::map<std::string, geom::ConvexPolygon> shapes_;
};

}  // namespace

int main(int argc, char** argv) {
  if (argc > 2) {
    std::cerr << "usage: minkcli [jobfile]   (stdin if omitted)\n";
    return 2;
  }
  try {
    if (argc == 2) {
      std::ifstream f(argv[1]);
      if (!f) {
        std::cerr << "cannot open job file: " << argv[1] << '\n';
        return 2;
      }
      Job job(f);
      return job.run();
    }
    Job job(std::cin);
    return job.run();
  } catch (const geom::GeometryError& e) {
    std::cerr << "geometry error: " << e.what() << '\n';
    return 3;
  } catch (const std::exception& e) {
    std::cerr << "error: " << e.what() << '\n';
    return 2;
  }
}
