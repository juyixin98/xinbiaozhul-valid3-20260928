#include "minkowski/config.h"
#include "minkowski/protocol.h"

#include <cstring>
#include <iostream>
#include <string>

int main(int argc, char** argv) {
    minkowski::ServiceConfig config;
    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--config" && i + 1 < argc) {
            config = minkowski::ServiceConfig::from_file(argv[++i]);
        } else if (arg == "--log-level" && i + 1 < argc) {
            config.log_level = minkowski::parse_log_level(argv[++i]);
        } else if (arg == "--stop-on-error") {
            config.continue_on_error = false;
        } else if (arg == "--quiet-logs") {
            config.log_level = minkowski::LogLevel::Quiet;
        } else if (arg == "--help") {
            std::cout <<
                "Usage: minkowski_service [--config PATH] "
                "[--log-level quiet|error|info|debug] "
                "[--stop-on-error] [--quiet-logs]\n";
            return 0;
        } else {
            std::cerr << "unknown argument: " << arg << '\n';
            return 2;
        }
    }

    minkowski::BatchService service(config, std::cin, std::cout, std::cerr);
    return service.run();
}
