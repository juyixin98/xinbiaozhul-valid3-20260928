#include "nearest/geometry/types.hpp"

#include <cmath>
#include <cstdlib>
#include <fstream>
#include <iostream>
#include <string>

namespace {

void emitVertex(std::ostream& out, long long x, long long y) {
    out << "V," << x << ',' << y << '\n';
}

void emitQuery(std::ostream& out, const std::string& id, long long x, long long y) {
    out << "Q," << id << ',' << x << ',' << y << '\n';
}

void emitQueryText(std::ostream& out, const std::string& id,
    const std::string& x, const std::string& y) {
    out << "Q," << id << ',' << x << ',' << y << '\n';
}

}  // namespace

int main(int argc, char** argv) {
    std::string output_path = "-";
    long long sawtooth_segments = 2048;

    for (int i = 1; i < argc; ++i) {
        std::string argument = argv[i];
        if ((argument == "--output" || argument == "-o") && i + 1 < argc) {
            output_path = argv[++i];
        } else if (argument == "--sawtooth-segments" && i + 1 < argc) {
            sawtooth_segments = std::atoll(argv[++i]);
        } else {
            std::cerr << "Usage: generate_fixtures [-o file] [--sawtooth-segments N]\n";
            return 2;
        }
    }

    if (sawtooth_segments < 8) {
        std::cerr << "sawtooth-segments must be at least 8\n";
        return 2;
    }

    std::ofstream file;
    if (output_path != "-") {
        file.open(output_path);
        if (!file) {
            std::cerr << "cannot open output: " << output_path << '\n';
            return 2;
        }
    }
    std::ostream& out = output_path == "-" ? std::cout : file;

    out << "# Synthetic local fixture. It exercises retained zero length,\n"
           "# perpendicular ties, endpoint ties, self-intersection, huge values.\n";

    // Retained zero-length segment is segment 0. The next segment ties for
    // query ZERO_TIE at (0,1), so expected candidate indices are 0 and 1.
    emitVertex(out, 0, 0);
    emitVertex(out, 0, 0);
    emitVertex(out, 2, 0);
    emitVertex(out, 2, 2);

    // Bow-tie self-intersection at (5,5). Segment 3 is (0,10)->(10,0).
    // Segments 5 and 7 cross at (500,500). The short connectors to that local
    // bow tie do not contain the intersection.
    emitVertex(out, 100, 100);
    emitVertex(out, 0, 1000);
    emitVertex(out, 1000, 0);
    emitVertex(out, 0, 0);
    emitVertex(out, 1000, 1000);

    // Deterministic sawtooth section. It is spatially separate from the first
    // cases and provides a large local-workload scan target.
    long long x = 20;
    for (long long i = 0; i < sawtooth_segments; ++i) {
        long long y = (i % 2 == 0) ? 0 : 4;
        x += 2;
        emitVertex(out, x, y);
    }

    // Integer values far above floating-point safe ranges. Exact rational
    // arithmetic preserves the midpoint and endpoint queries.
    const std::string huge = "100000000000000000000000000000";
    out << "V," << huge << ',' << huge << '\n';
    out << "V," << "300000000000000000000000000000" << ','
        << "300000000000000000000000000000" << '\n';

    emitQuery(out, "ZERO_TIE", 0, 1);
    emitQuery(out, "CORNER_TIE", 1, 1);
    emitQuery(out, "ENDPOINT", 2, 3);
    emitQuery(out, "SELF_INTERSECTION", 500, 500);
    emitQuery(out, "SAWTOOTH_INTERIOR", 23, 2);
    emitQueryText(out, "HUGE_MIDPOINT", "200000000000000000000000000001",
        "199999999999999999999999999999");
    emitQueryText(out, "HUGE_ENDPOINT", "300000000000000000000000000000",
        "300000000000000000000000000000");

    return 0;
}
