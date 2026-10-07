#include "nearest/service/configuration.hpp"

#include <sstream>

namespace nearest::service {

Configuration defaultConfiguration() {
    return Configuration{};
}

ConfigurationParseResult parseConfiguration(const std::string& text) {
    Configuration configuration = defaultConfiguration();
    std::istringstream input(text);
    std::string line;
    std::size_t line_number = 0;

    while (std::getline(input, line)) {
        ++line_number;
        if (!line.empty() && line.back() == '\r') {
            line.pop_back();
        }
        std::size_t hash = line.find('#');
        if (hash != std::string::npos) {
            line.erase(hash);
        }
        if (line.find_first_not_of(" \t") == std::string::npos) {
            continue;
        }

        std::size_t equals = line.find('=');
        if (equals == std::string::npos) {
            return {configuration, "line " + std::to_string(line_number) + ": expected key=value"};
        }

        auto trimmed = [](std::string value) {
            std::size_t first = value.find_first_not_of(" \t");
            std::size_t last = value.find_last_not_of(" \t");
            if (first == std::string::npos) {
                return std::string{};
            }
            return value.substr(first, last - first + 1U);
        };

        std::string key = trimmed(line.substr(0, equals));
        std::string value = trimmed(line.substr(equals + 1U));

        if (key == "zero_length_policy") {
            if (value == "retain") {
                configuration.zero_length_policy = ZeroLengthPolicy::Retain;
            } else if (value == "reject") {
                configuration.zero_length_policy = ZeroLengthPolicy::Reject;
            } else {
                return {configuration, "line " + std::to_string(line_number) +
                    ": zero_length_policy must be retain or reject"};
            }
        } else if (key == "tie_policy") {
            if (value == "all") {
                configuration.tie_policy = TiePolicy::AllCandidates;
            } else if (value == "first") {
                configuration.tie_policy = TiePolicy::FirstCandidate;
            } else {
                return {configuration, "line " + std::to_string(line_number) +
                    ": tie_policy must be all or first"};
            }
        } else if (key == "decimal_mileage") {
            if (value == "true") {
                configuration.decimal_mileage = true;
            } else if (value == "false") {
                configuration.decimal_mileage = false;
            } else {
                return {configuration, "line " + std::to_string(line_number) +
                    ": decimal_mileage must be true or false"};
            }
        } else {
            return {configuration, "line " + std::to_string(line_number) +
                ": unknown configuration key '" + key + "'"};
        }
    }

    return {configuration, ""};
}

std::string configurationValidationError(const Configuration& configuration) {
    if (!configuration.decimal_mileage) {
        return "decimal_mileage=false is intentionally unsupported because mileage may require sqrt";
    }
    return "";
}

}  // namespace nearest::service
