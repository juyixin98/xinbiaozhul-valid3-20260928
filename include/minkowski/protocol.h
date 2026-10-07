#pragma once

#include "minkowski/config.h"
#include "minkowski/logger.h"
#include "minkowski/types.h"

#include <istream>
#include <map>
#include <ostream>
#include <string>

namespace minkowski {

class BatchService {
public:
    BatchService(ServiceConfig config, std::istream& in,
                 std::ostream& out, std::ostream& err);

    int run();

private:
    struct Command {
        std::string name;
        std::vector<std::string> tokens;
    };

    void execute(const Command& command, const std::string& raw_line);
    void define_set(const std::string& name, const std::vector<Point>& points);
    std::string format_points(const std::vector<Point>& points) const;

    ServiceConfig config_;
    std::istream& in_;
    std::ostream& out_;
    std::ostream& err_;
    ProgressLogger logger_;
    std::map<std::string, ConvexSet> sets_;
};

} // namespace minkowski
