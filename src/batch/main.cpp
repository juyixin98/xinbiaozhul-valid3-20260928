#include "nearest/io/batch_io.hpp"
#include "nearest/service/configuration.hpp"
#include "nearest/service/diagnostics.hpp"
#include "nearest/service/nearest_projection_service.hpp"
#include "nearest/service/polyline_projector.hpp"

#include <fstream>
#include <iostream>
#include <sstream>

using namespace nearest;

namespace {

std::string readFileOrStdin(const std::string& path, std::ifstream& file) {
    if (path == "-") {
        return {};
    }
    file.open(path);
    if (!file) {
        return "cannot open input file: " + path;
    }
    return {};
}

}  // namespace

int main(int argc, char** argv) {
    std::string input_path = "-";
    std::string output_path = "-";
    std::string config_path;

    for (int i = 1; i < argc; ++i) {
        std::string argument = argv[i];
        if ((argument == "--input" || argument == "-i") && i + 1 < argc) {
            input_path = argv[++i];
        } else if ((argument == "--output" || argument == "-o") && i + 1 < argc) {
            output_path = argv[++i];
        } else if ((argument == "--config" || argument == "-c") && i + 1 < argc) {
            config_path = argv[++i];
        } else if (argument == "--help" || argument == "-h") {
            std::cout << "Usage: nearest_projection_batch [-i input.csv] [-o output.csv] "
                         "[-c config.properties]\n";
            return 0;
        } else {
            std::cerr << "unknown argument: " << argument << '\n';
            return 2;
        }
    }

    service::Configuration configuration = service::defaultConfiguration();
    if (!config_path.empty()) {
        std::ifstream config_file(config_path);
        if (!config_file) {
            std::cerr << "cannot open configuration: " << config_path << '\n';
            return 2;
        }
        std::stringstream buffer;
        buffer << config_file.rdbuf();
        service::ConfigurationParseResult parsed = service::parseConfiguration(buffer.str());
        if (!parsed.error.empty()) {
            std::cerr << "configuration error: " << parsed.error << '\n';
            return 2;
        }
        configuration = parsed.configuration;
        std::string validation = service::configurationValidationError(configuration);
        if (!validation.empty()) {
            std::cerr << "unsupported configuration: " << validation << '\n';
            return 2;
        }
    }

    std::ifstream input_file;
    std::string open_error = readFileOrStdin(input_path, input_file);
    if (!open_error.empty()) {
        std::cerr << open_error << '\n';
        return 2;
    }
    std::istream& input = input_path == "-" ? std::cin : input_file;

    io::BatchParseResult parsed = io::parseBatchInput(input);
    if (!parsed.ok) {
        std::cerr << "input error at line " << parsed.failure.line_number
                  << " [" << failureName(parsed.failure.category) << "]: "
                  << parsed.failure.message << '\n';
        return 1;
    }

    service::PolylineProjector projector;
    service::ProjectorBuildResult build = projector.build(
        std::move(parsed.data.polyline), configuration);
    if (build.category != FailureCategory::None) {
        std::cerr << "polyline rejected [" << failureName(build.category)
                  << "]: " << build.message << '\n';
        return 1;
    }

    service::NearestProjectionService service(std::move(projector), configuration);
    std::ofstream output_file;
    if (output_path != "-") {
        output_file.open(output_path);
        if (!output_file) {
            std::cerr << "cannot open output file: " << output_path << '\n';
            return 2;
        }
    }
    std::ostream& output = output_path == "-" ? std::cout : output_file;

    bool first = true;
    for (const service::QueryRequest& request : parsed.data.requests) {
        service::RequestResult result = service.process(request);
        if (first) {
            output << "request_id,status,failure_category,candidate_rank,candidate_count,"
                      "segment_index,t,x,y,distance_squared,start_mileage,mileage,"
                      "segment_length,nodes_visited,nodes_pruned,leaves_visited\n";
            first = false;
        }

        service.emitResult(output, result);
        service::DiagnosticRecord diagnostic{
            result.request.request_id,
            result.decision,
            result.category,
            result.candidates.size(),
            result.nodes_visited,
            result.nodes_pruned,
            result.leaves_visited,
            result.message
        };
        service::writeDiagnostic(std::cerr, diagnostic);
    }

    return 0;
}
