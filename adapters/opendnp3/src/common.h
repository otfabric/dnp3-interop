// SPDX-License-Identifier: MIT
//
// Shared declarations of the opendnp3 adapter. See docs/CONTAINER_CONTRACT.md.

#pragma once

#include <nlohmann/json.hpp>

#include <cstdint>
#include <map>
#include <optional>
#include <set>
#include <string>
#include <vector>

namespace adapter
{

using json = nlohmann::ordered_json;

constexpr const char* kAdapter = "opendnp3";
constexpr const char* kSchemaVersion = "1.0";
constexpr const char* kDefaultFixture = "/fixtures/baseline/fixture.json";
constexpr const char* kDefaultReadyFile = "/run/dnp3-interop/ready";

enum Exit : int
{
    kExitOK = 0,
    kExitFailed = 1,
    kExitUsage = 2,
    kExitConnectFailed = 3,
    kExitFixtureInvalid = 4,
};

// Thrown by argument parsing: exit 2 with the message on stderr.
struct UsageError
{
    std::string message;
};

// Prints one JSON document on one line of stdout (thread-safe).
void emit(const json& doc);
// Diagnostics on stderr.
void logf(const char* format, ...) __attribute__((format(printf, 1, 2)));

uint64_t now_ms();
std::string format_time(uint64_t ms); // YYYY-MM-DDTHH:MM:SS.mmmZ

// Names of the contract.
json iin_names(uint8_t lsb, uint8_t msb);
json flag_names(uint8_t group, uint8_t flags);
// Bit of a flag name for a point type of the fixture; -1 when unknown.
int flag_bit(const std::string& pointType, const std::string& name);
std::string command_status_name(uint8_t status);
std::string to_hex(const uint8_t* data, size_t length);

// Command line: "--name value" flags and bare "--name" switches.
class Args
{
public:
    Args(const std::vector<std::string>& argv, const std::set<std::string>& flags, const std::set<std::string>& switches);

    bool has(const std::string& name) const;
    std::string str(const std::string& name, const std::string& def) const;
    std::string required(const std::string& name) const;
    int64_t integer(const std::string& name, int64_t def, int64_t min, int64_t max) const;
    double number(const std::string& name) const;
    std::vector<int> classes(const std::string& name, const std::string& def, int min) const;

private:
    std::map<std::string, std::string> values;
};

// ---- fixture ----

struct Point
{
    uint16_t index = 0;
    double value = 0;               // numeric types; booleans as 0/1; double-bit state
    std::vector<uint8_t> octets;    // octet strings
    uint8_t flags = 0x01;
    int clazz = 0;
    int svar = 0;
    int evar = 0;
    std::optional<uint16_t> target;
    bool selectRequired = false;
    std::optional<double> min, max;
};

struct Fixture
{
    std::string name;
    std::string text; // the file, byte for byte
    uint16_t address = 0;
    uint16_t masterAddress = 0;
    bool unsolicited = false;
    bool needTime = false;
    std::optional<uint16_t> coldRestartDelayMs, warmRestartDelayMs;
    std::optional<uint16_t> maxControlsPerRequest; // contract 1.3
    std::map<std::string, std::vector<Point>> points; // by array name

    const Point* find(const std::string& type, uint16_t index) const;
};

// Loads and validates a fixture with the rules of docs/FIXTURES.md. Returns
// an error message, empty on success.
std::string load_fixture(const std::string& path, Fixture& out);

int run_server(const std::vector<std::string>& args);
int run_client(const std::vector<std::string>& args);

} // namespace adapter
