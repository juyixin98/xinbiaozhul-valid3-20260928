#include "minkowski/convex_hull.h"
#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"
#include "minkowski/support.h"
#include "minkowski/verification.h"

#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>

int main(int argc, char** argv) {
    const std::filesystem::path output_dir =
        argc > 1 ? argv[1] : "examples/generated";
    std::filesystem::create_directories(output_dir);

    std::ofstream batch(output_dir / "batch.txt");
    if (!batch) throw std::runtime_error("cannot write batch fixture");
    batch <<
        "# Synthetic local batch: two unit squares and a triangle.\n"
        "RUN generated-fixture-001\n"
        "POLY A 0 0 1 0 1 1 0 1\n"
        "POLY B 2 3 3 3 3 4 2 4\n"
        "POLY T 0 0 4 0 0 3\n"
        "SUM S A B VERIFY\n"
        "TRANSLATE U T -2 5 VERIFY\n"
        "SUPPORT S 1 1\n"
        "SUPPORT T 4 3\n"
        "VERIFY_SUPPORT A B 7 -2\n";

    std::ofstream degenerates(output_dir / "degenerates.txt");
    degenerates <<
        "RUN generated-fixture-002\n"
        "POLY P 5 -6\n"
        "POLY H 0 0 3 0\n"
        "POLY V 0 0 0 2\n"
        "SUM PS P H VERIFY\n"
        "SUM BOX H V VERIFY\n"
        "SUPPORT BOX -1 1\n";

    std::ofstream failures(output_dir / "expected-failures.txt");
    failures <<
        "RUN generated-fixture-003\n"
        "# Concave input must be categorized as NON_CONVEX.\n"
        "POLY BAD 0 0 3 0 3 3 2 1 0 3\n"
        "# This line still runs because continue-on-error defaults true.\n"
        "POLY GOOD 0 0 1 0 0 1\n";

    // A compact machine-readable expected answer file is also generated using
    // exact integer values independently written here.
    std::ofstream expected(output_dir / "expected.txt");
    expected <<
        "case=batch sum_square=2,3;4,3;4,5;2,5 support_s=9\n"
        "case=degenerates h_plus_v=0,0;3,0;3,2;0,2 support_box=2\n";

    std::cout << "generated fixtures in " << output_dir << '\n';
    return 0;
}
