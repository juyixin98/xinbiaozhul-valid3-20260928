#pragma once

#include <string>

namespace nearest::service {

enum class ZeroLengthPolicy {
    Retain,
    Reject
};

enum class TiePolicy {
    AllCandidates,
    FirstCandidate
};

struct Configuration {
    ZeroLengthPolicy zero_length_policy = ZeroLengthPolicy::Retain;
    TiePolicy tie_policy = TiePolicy::AllCandidates;
    bool decimal_mileage = true;
};

struct ConfigurationParseResult {
    Configuration configuration;
    std::string error;
};

Configuration defaultConfiguration();
ConfigurationParseResult parseConfiguration(const std::string& text);
std::string configurationValidationError(const Configuration& configuration);

}  // namespace nearest::service
