// SPDX-License-Identifier: MIT

#include "common.h"

#include <chrono>
#include <cmath>
#include <cstdarg>
#include <cstdio>
#include <ctime>
#include <iostream>
#include <mutex>
#include <sstream>

namespace adapter
{

namespace
{
std::mutex out_mutex;
} // namespace

void emit(const json& doc)
{
    const auto line = doc.dump(-1, ' ', false, json::error_handler_t::replace);
    std::lock_guard<std::mutex> lock(out_mutex);
    std::fwrite(line.data(), 1, line.size(), stdout);
    std::fputc('\n', stdout);
    std::fflush(stdout);
}

void logf(const char* format, ...)
{
    char buffer[1024];
    va_list args;
    va_start(args, format);
    std::vsnprintf(buffer, sizeof(buffer), format, args);
    va_end(args);
    std::fprintf(stderr, "[%s] %s\n", kAdapter, buffer);
}

uint64_t now_ms()
{
    using namespace std::chrono;
    return static_cast<uint64_t>(duration_cast<milliseconds>(system_clock::now().time_since_epoch()).count());
}

std::string format_time(uint64_t ms)
{
    const std::time_t seconds = static_cast<std::time_t>(ms / 1000);
    std::tm tm{};
    gmtime_r(&seconds, &tm);
    // Large enough for any int the fields can hold, which GCC checks.
    char buffer[96];
    std::snprintf(buffer, sizeof(buffer), "%04d-%02d-%02dT%02d:%02d:%02d.%03dZ", tm.tm_year + 1900, tm.tm_mon + 1,
                  tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec, static_cast<int>(ms % 1000));
    return buffer;
}

json iin_names(uint8_t lsb, uint8_t msb)
{
    static const char* names[16] = {"BROADCAST",
                                    "CLASS_1_EVENTS",
                                    "CLASS_2_EVENTS",
                                    "CLASS_3_EVENTS",
                                    "NEED_TIME",
                                    "LOCAL_CONTROL",
                                    "DEVICE_TROUBLE",
                                    "DEVICE_RESTART",
                                    "NO_FUNC_CODE_SUPPORT",
                                    "OBJECT_UNKNOWN",
                                    "PARAMETER_ERROR",
                                    "EVENT_BUFFER_OVERFLOW",
                                    "ALREADY_EXECUTING",
                                    "CONFIG_CORRUPT",
                                    "RESERVED_2",
                                    "RESERVED_1"};
    json out = json::array();
    const uint16_t bits = static_cast<uint16_t>(lsb | (msb << 8));
    for (int i = 0; i < 16; ++i)
    {
        if (bits & (1u << i))
            out.push_back(names[i]);
    }
    return out;
}

namespace
{
// Bits 5 and 6 differ by type family; 0..4 are common.
const char* common_flags[5] = {"ONLINE", "RESTART", "COMM_LOST", "REMOTE_FORCED", "LOCAL_FORCED"};

enum class Family
{
    Binary,
    Counter,
    Analog,
};

Family family_of_group(uint8_t group)
{
    switch (group)
    {
    case 20:
    case 21:
    case 22:
    case 23:
        return Family::Counter;
    case 30:
    case 32:
    case 40:
    case 42:
        return Family::Analog;
    default:
        return Family::Binary;
    }
}

const char* high_flag(Family family, int bit, bool outputs)
{
    if (family == Family::Counter)
        return bit == 5 ? "ROLLOVER" : "DISCONTINUITY";
    if (family == Family::Analog)
        return bit == 5 ? "OVER_RANGE" : "REFERENCE_ERR";
    return (bit == 5 && !outputs) ? "CHATTER_FILTER" : nullptr;
}
} // namespace

json flag_names(uint8_t group, uint8_t flags)
{
    json out = json::array();
    for (int bit = 0; bit < 5; ++bit)
    {
        if (flags & (1u << bit))
            out.push_back(common_flags[bit]);
    }
    const auto family = family_of_group(group);
    const bool outputs = group == 10 || group == 11;
    for (int bit = 5; bit < 7; ++bit)
    {
        if (!(flags & (1u << bit)))
            continue;
        if (family == Family::Binary && (group == 3 || group == 4) && bit == 6)
            continue; // a state bit of the double-bit types
        if (auto name = high_flag(family, bit, outputs))
            out.push_back(name);
    }
    return out;
}

int flag_bit(const std::string& pointType, const std::string& name)
{
    for (int bit = 0; bit < 5; ++bit)
    {
        if (name == common_flags[bit])
            return bit;
    }
    Family family = Family::Binary;
    if (pointType == "counters" || pointType == "frozenCounters")
        family = Family::Counter;
    else if (pointType == "analogInputs" || pointType == "analogOutputs")
        family = Family::Analog;
    const bool outputs = pointType == "binaryOutputs";
    for (int bit = 5; bit < 7; ++bit)
    {
        // ROLLOVER is obsolete and not accepted in fixtures.
        if (family == Family::Counter && bit == 5)
            continue;
        auto flag = high_flag(family, bit, outputs);
        if (flag && name == flag)
            return bit;
    }
    return -1;
}

std::string command_status_name(uint8_t status)
{
    static const char* names[] = {"SUCCESS",
                                  "TIMEOUT",
                                  "NO_SELECT",
                                  "FORMAT_ERROR",
                                  "NOT_SUPPORTED",
                                  "ALREADY_ACTIVE",
                                  "HARDWARE_ERROR",
                                  "LOCAL",
                                  "TOO_MANY_OPS",
                                  "NOT_AUTHORIZED",
                                  "AUTOMATION_INHIBIT",
                                  "PROCESSING_LIMITED",
                                  "OUT_OF_RANGE",
                                  "DOWNSTREAM_LOCAL",
                                  "ALREADY_COMPLETE",
                                  "BLOCKED",
                                  "CANCELLED",
                                  "BLOCKED_OTHER_MASTER",
                                  "DOWNSTREAM_FAIL"};
    if (status < sizeof(names) / sizeof(names[0]))
        return names[status];
    if (status == 126)
        return "NON_PARTICIPATING";
    return "UNDEFINED";
}

std::string to_hex(const uint8_t* data, size_t length)
{
    static const char* digits = "0123456789abcdef";
    std::string out;
    out.reserve(length * 2);
    for (size_t i = 0; i < length; ++i)
    {
        out.push_back(digits[data[i] >> 4]);
        out.push_back(digits[data[i] & 0x0f]);
    }
    return out;
}

// ---- Args ----

Args::Args(const std::vector<std::string>& argv, const std::set<std::string>& flags, const std::set<std::string>& switches)
{
    for (size_t i = 0; i < argv.size(); ++i)
    {
        const auto& arg = argv[i];
        if (arg.rfind("--", 0) != 0)
            throw UsageError{"unexpected argument '" + arg + "'"};
        const auto name = arg.substr(2);
        if (switches.count(name))
        {
            values[name] = "true";
        }
        else if (flags.count(name))
        {
            if (i + 1 >= argv.size())
                throw UsageError{"--" + name + " needs a value"};
            values[name] = argv[++i];
        }
        else
        {
            throw UsageError{"unknown flag --" + name};
        }
    }
}

bool Args::has(const std::string& name) const
{
    return values.count(name) != 0;
}

std::string Args::str(const std::string& name, const std::string& def) const
{
    auto it = values.find(name);
    return it == values.end() ? def : it->second;
}

std::string Args::required(const std::string& name) const
{
    auto it = values.find(name);
    if (it == values.end())
        throw UsageError{"--" + name + " is required"};
    return it->second;
}

int64_t Args::integer(const std::string& name, int64_t def, int64_t min, int64_t max) const
{
    auto it = values.find(name);
    if (it == values.end())
        return def;
    try
    {
        size_t used = 0;
        const auto v = std::stoll(it->second, &used, 10);
        if (used != it->second.size() || v < min || v > max)
            throw UsageError{};
        return v;
    }
    catch (...)
    {
        throw UsageError{"--" + name + " must be an integer in " + std::to_string(min) + ".." + std::to_string(max)};
    }
}

double Args::number(const std::string& name) const
{
    const auto text = required(name);
    try
    {
        size_t used = 0;
        const auto v = std::stod(text, &used);
        if (used != text.size() || !std::isfinite(v))
            throw UsageError{};
        return v;
    }
    catch (...)
    {
        throw UsageError{"--" + name + " must be a number"};
    }
}

std::vector<int> Args::classes(const std::string& name, const std::string& def, int min) const
{
    std::vector<int> out;
    std::stringstream in(str(name, def));
    std::string item;
    while (std::getline(in, item, ','))
    {
        if (item.size() != 1 || item[0] < '0' + min || item[0] > '3')
            throw UsageError{"--" + name + " is a comma-separated list of classes " + std::to_string(min) + "..3"};
        out.push_back(item[0] - '0');
    }
    if (out.empty())
        throw UsageError{"--" + name + " is empty"};
    return out;
}

} // namespace adapter
