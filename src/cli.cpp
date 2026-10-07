#include "mbr/batch.hpp"
#include "mbr/solver.hpp"

#include <fstream>
#include <iostream>
#include <sstream>

int main(int argc, char** argv) {
    std::string input_path = "-";
    std::string output_path = "-";
    mbr::SolverOptions defaults;

    for (int i = 1; i < argc; ++i) {
        std::string argument = argv[i];
        auto require_value = [&](const std::string& name) {
            if (i + 1 >= argc) {
                throw std::invalid_argument("missing value for " + name);
            }
            return std::string(argv[++i]);
        };
        if (argument == "--input") {
            input_path = require_value(argument);
        } else if (argument == "--output") {
            output_path = require_value(argument);
        } else if (argument == "--max-points") {
            defaults.max_input_points =
                static_cast<std::size_t>(std::stoull(require_value(argument)));
        } else if (argument == "--near-collinear-tolerance") {
            auto value = mbr::Rational::parse(require_value(argument));
            if (!value) throw std::invalid_argument("invalid tolerance");
            defaults.near_collinear_tolerance = *value;
        } else if (argument == "--decimals") {
            defaults.output_decimals = std::stoi(require_value(argument));
        } else if (argument == "--help") {
            std::cout <<
                "Usage: mbr_batch [--input PATH|-] [--output PATH|-]\n"
                "                  [--max-points N] [--decimals N]\n"
                "                  [--near-collinear-tolerance RATIONAL]\n"
                "Input is JSON Lines. '-' means stdin/stdout.\n";
            return 0;
        } else {
            std::cerr << "unknown argument: " << argument << '\n';
            return 2;
        }
    }

    try {
        std::stringstream buffer;
        if (input_path == "-") {
            buffer << std::cin.rdbuf();
        } else {
            std::ifstream file(input_path);
            if (!file) {
                std::cerr << "cannot open input: " << input_path << '\n';
                return 2;
            }
            buffer << file.rdbuf();
        }

        mbr::BatchResult result =
            mbr::process_batch_input(buffer.str(), defaults);
        std::ofstream output_file;
        std::ostream* output = &std::cout;
        if (output_path != "-") {
            output_file.open(output_path);
            if (!output_file) {
                std::cerr << "cannot open output: " << output_path << '\n';
                return 2;
            }
            output = &output_file;
        }
        for (const std::string& line : result.output_lines) {
            *output << line << '\n';
        }
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "startup_error: " << error.what() << '\n';
        return 2;
    }
}
