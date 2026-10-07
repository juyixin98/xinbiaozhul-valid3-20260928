#include "minkowski/protocol.h"

#include "minkowski/convex_set.h"
#include "minkowski/minkowski.h"
#include "minkowski/support.h"
#include "minkowski/verification.h"

#include <algorithm>
#include <cstdlib>
#include <sstream>
#include <stdexcept>
#include <utility>

namespace minkowski {
namespace {

std::vector<std::string> tokenize(const std::string& line) {
    std::istringstream input(line);
    std::vector<std::string> tokens;
    std::string token;
    while (input >> token) tokens.push_back(token);
    return tokens;
}

Integer parse_integer(const std::string& token) {
    try {
        return Integer(token);
    } catch (const std::exception&) {
        throw std::runtime_error("not an exact integer: " + token);
    }
}

Point parse_point(const std::string& x, const std::string& y) {
    return {parse_integer(x), parse_integer(y)};
}

void require_arity(const std::vector<std::string>& tokens,
                   std::size_t expected, const char* syntax) {
    if (tokens.size() != expected) {
        throw std::runtime_error(std::string("syntax: ") + syntax);
    }
}

} // namespace

BatchService::BatchService(ServiceConfig config, std::istream& in,
                           std::ostream& out, std::ostream& err)
    : config_(std::move(config)), in_(in), out_(out), err_(err),
      logger_(config_, out, err) {}

std::string BatchService::format_points(
    const std::vector<Point>& points) const {
    std::ostringstream result;
    result << points.size();
    for (const Point& p : points) {
        result << " " << p.x.str() << " " << p.y.str();
    }
    return result.str();
}

void BatchService::define_set(const std::string& name,
                              const std::vector<Point>& points) {
    const ValidationResult validation = validate_and_normalize(points);
    if (!validation.ok) {
        logger_.detail("validation category=" +
                       to_string(validation.error) +
                       " input-point-count=" +
                       std::to_string(points.size()));
        throw std::runtime_error(to_string(validation.error));
    }
    sets_[name] = validation.value;
    logger_.detail("normalized set=" + name + " kind=" +
                   to_string(validation.value.kind) +
                   " vertex-count=" +
                   std::to_string(validation.value.vertices.size()));
}

int BatchService::run() {
    out_ << "READY service=minkowski-service version=1.0.0 boost=1.74+\n";
    logger_.info("starting exact convex-set batch service");

    int failures = 0;
    std::string raw_line;
    while (std::getline(in_, raw_line)) {
        const std::vector<std::string> tokens = tokenize(raw_line);
        if (tokens.empty() || tokens.front().starts_with("#")) continue;

        Command command{tokens.front(), tokens};
        logger_.command(raw_line);
        const std::size_t before = failures;
        try {
            execute(command, raw_line);
        } catch (const std::exception& ex) {
            ++failures;
            logger_.error("COMMAND_FAILED", ex.what());
            out_ << "ERROR category=COMMAND_FAILED reason="
                 << ex.what() << "\n";
        }

        if (static_cast<std::size_t>(failures) != before &&
            !config_.continue_on_error) {
            out_ << "STOPPED continue-on-error=false failures="
                 << failures << "\n";
            return 1;
        }
    }

    out_ << "DONE failures=" << failures << "\n";
    return failures == 0 ? 0 : 1;
}

void BatchService::execute(const Command& command,
                           const std::string& raw_line) {
    const std::vector<std::string>& t = command.tokens;

    if (t.front() == "RUN") {
        require_arity(t, 2, "RUN <run-id>");
        logger_.set_run_id(t[1]);
        out_ << "OK RUN run-id=" << t[1] << "\n";
        return;
    }
    if (t.front() == "POLY") {
        if (t.size() < 4 || (t.size() % 2) != 0) {
            throw std::runtime_error("syntax: POLY <name> x y [x y ...]");
        }
        std::vector<Point> points;
        for (std::size_t i = 2; i < t.size(); i += 2) {
            points.push_back(parse_point(t[i], t[i + 1]));
        }
        define_set(t[1], points);
        out_ << "OK POLY name=" << t[1] << " "
             << to_string(sets_[t[1]]) << "\n";
        return;
    }
    if (t.front() == "SHOW") {
        require_arity(t, 2, "SHOW <name>");
        const auto it = sets_.find(t[1]);
        if (it == sets_.end()) throw std::runtime_error("unknown set: " + t[1]);
        out_ << "OK SHOW name=" << t[1] << " "
             << to_string(it->second) << "\n";
        return;
    }
    if (t.front() == "LIST") {
        require_arity(t, 1, "LIST");
        out_ << "OK LIST count=" << sets_.size();
        for (const auto& [name, value] : sets_) {
            out_ << " " << name << ":" << to_string(value.kind);
        }
        out_ << "\n";
        return;
    }
    if (t.front() == "SUM") {
        if (t.size() != 4 && t.size() != 5) {
            throw std::runtime_error(
                "syntax: SUM <out> <a> <b> VERIFY?");
        }
        const auto ia = sets_.find(t[2]);
        const auto ib = sets_.find(t[3]);
        if (ia == sets_.end() || ib == sets_.end()) {
            throw std::runtime_error("unknown SUM operand");
        }
        const bool verify = t.size() == 5;
        if (verify && t[4] != "VERIFY") {
            throw std::runtime_error("SUM optional token must be VERIFY");
        }
        ConvexSet sum = minkowski_sum(ia->second, ib->second);
        logger_.detail("Minkowski edge-merge completed result-vertices=" +
                       std::to_string(sum.vertices.size()));
        sets_[t[1]] = sum;
        out_ << "OK SUM name=" << t[1] << " " << to_string(sum) << "\n";
        if (verify) {
            const auto report = verify_minkowski(sum, ia->second, ib->second);
            logger_.detail(report.rule + " " + report.reason);
            out_ << (report.passed ? "VERIFY PASS" : "VERIFY FAIL")
                 << " rule=" << report.rule
                 << " evidence=" << report.reason << "\n";
            if (!report.passed) {
                throw std::runtime_error("Minkowski reference verification failed");
            }
        }
        return;
    }
    if (t.front() == "TRANSLATE") {
        if (t.size() != 5 && t.size() != 6) {
            throw std::runtime_error(
                "syntax: TRANSLATE <out> <input> dx dy VERIFY?");
        }
        const auto input = sets_.find(t[2]);
        if (input == sets_.end()) {
            throw std::runtime_error("unknown TRANSLATE input: " + t[2]);
        }
        const Point offset = parse_point(t[3], t[4]);
        ConvexSet translated = translate(input->second, offset);
        sets_[t[1]] = translated;
        out_ << "OK TRANSLATE name=" << t[1] << " "
             << to_string(translated) << "\n";
        const bool verify = t.size() == 6;
        if (verify && t[5] != "VERIFY") {
            throw std::runtime_error("TRANSLATE optional token must be VERIFY");
        }
        if (verify) {
            const auto report = verify_translation(
                translated, input->second, offset);
            out_ << (report.passed ? "VERIFY PASS" : "VERIFY FAIL")
                 << " rule=" << report.rule
                 << " evidence=" << report.reason << "\n";
            if (!report.passed) {
                throw std::runtime_error("translation verification failed");
            }
        }
        return;
    }
    if (t.front() == "SUPPORT") {
        require_arity(t, 4, "SUPPORT <name> dx dy");
        const auto input = sets_.find(t[1]);
        if (input == sets_.end()) {
            throw std::runtime_error("unknown SUPPORT set: " + t[1]);
        }
        const Point direction = parse_point(t[2], t[3]);
        const auto result = support(input->second, direction);
        logger_.detail("support evaluated criterion=dot(point,direction) "
                       "maximizer-count=" +
                       std::to_string(result.maximizers.size()));
        out_ << "OK SUPPORT set=" << t[1]
             << " direction=" << t[2] << " " << t[3]
             << " value=" << result.value.str()
             << " maximizers=" << format_points(result.maximizers) << "\n";
        return;
    }
    if (t.front() == "VERIFY_SUPPORT") {
        require_arity(t, 5, "VERIFY_SUPPORT <a> <b> dx dy");
        const auto ia = sets_.find(t[1]);
        const auto ib = sets_.find(t[2]);
        if (ia == sets_.end() || ib == sets_.end()) {
            throw std::runtime_error("unknown VERIFY_SUPPORT operand");
        }
        const Point direction = parse_point(t[3], t[4]);
        const auto report = verify_support_additivity(
            ia->second, ib->second, direction);
        out_ << (report.passed ? "VERIFY PASS" : "VERIFY FAIL")
             << " rule=" << report.rule
             << " evidence=" << report.reason << "\n";
        if (!report.passed) {
            throw std::runtime_error("support additivity verification failed");
        }
        return;
    }
    if (t.front() == "CONFIG") {
        require_arity(t, 1, "CONFIG");
        std::ostringstream values;
        config_.write(values);
        out_ << "OK CONFIG " << values.str() << "\n";
        return;
    }
    if (t.front() == "QUIT" || t.front() == "EXIT") {
        require_arity(t, 1, "QUIT");
        out_ << "OK QUIT\n";
        std::exit(0);
    }
    if (t.front() == "HELP") {
        require_arity(t, 1, "HELP");
        out_ << "OK HELP commands: POLY, SHOW, LIST, SUM, TRANSLATE, "
             << "SUPPORT, VERIFY_SUPPORT, CONFIG, RUN, QUIT\n";
        return;
    }

    throw std::runtime_error("unknown command: " + raw_line);
}

} // namespace minkowski
