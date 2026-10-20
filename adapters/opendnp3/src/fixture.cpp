// SPDX-License-Identifier: MIT
//
// Loads a fixture and applies the rules of fixtures/schema/dnp3-fixture.schema.json
// and of docs/FIXTURES.md ("cross-field rules"), as scripts/validate-fixtures.py does.

#include "common.h"

#include <cmath>
#include <fstream>
#include <regex>
#include <sstream>

namespace adapter
{

namespace
{

struct TypeRule
{
    const char* name;
    std::set<int> svars; // empty: no variation fields (octet strings)
    std::set<int> evars;
    int sdefault;
    int edefault;
};

const std::vector<TypeRule>& type_rules()
{
    static const std::vector<TypeRule> rules = {
        {"binaryInputs", {1, 2}, {1, 2}, 2, 2},
        {"doubleBitInputs", {2}, {1, 2}, 2, 2},
        {"binaryOutputs", {2}, {1, 2}, 2, 2},
        {"counters", {1, 2, 5, 6}, {1, 2, 5, 6}, 1, 1},
        {"frozenCounters", {1, 2, 5, 6, 9, 10}, {1, 2, 5, 6}, 1, 1},
        {"analogInputs", {1, 2, 3, 4, 5, 6}, {1, 2, 3, 4, 5, 6, 7, 8}, 1, 1},
        {"analogOutputs", {1, 2, 3, 4}, {1, 2, 3, 4, 5, 6, 7, 8}, 1, 1},
        {"octetStrings", {}, {}, 0, 0},
    };
    return rules;
}

bool is_integer(const json& v)
{
    return v.is_number_integer() || v.is_number_unsigned();
}

bool is_float32(double v)
{
    return static_cast<double>(static_cast<float>(v)) == v;
}

class Checker
{
public:
    std::vector<std::string> errors;
    void add(const std::string& e) { errors.push_back(e); }
};

void check_point(const TypeRule& rule, const json& p, size_t position, Point& out, Checker& c)
{
    const std::string type = rule.name;
    std::string where = type + "[" + std::to_string(position) + "]";
    if (!p.is_object())
    {
        c.add(where + ": not an object");
        return;
    }
    std::set<std::string> allowed = {"index", "value", "class"};
    if (type != "octetStrings")
        allowed.insert({"flags", "staticVariation", "eventVariation"});
    if (type == "binaryOutputs" || type == "analogOutputs")
        allowed.insert({"target", "selectRequired"});
    if (type == "analogOutputs")
        allowed.insert({"min", "max"});
    for (auto it = p.begin(); it != p.end(); ++it)
    {
        if (!allowed.count(it.key()))
            c.add(where + ": unknown field '" + it.key() + "'");
    }

    if (!p.contains("index") || !is_integer(p["index"]) || p["index"].get<int64_t>() < 0 || p["index"].get<int64_t>() > 65535)
    {
        c.add(where + ": index must be an integer in 0..65535");
        return;
    }
    out.index = static_cast<uint16_t>(p["index"].get<int64_t>());
    where = type + "[" + std::to_string(out.index) + "]";

    if (p.contains("class"))
    {
        if (!is_integer(p["class"]) || p["class"].get<int64_t>() < 0 || p["class"].get<int64_t>() > 3)
            c.add(where + ": class must be 0..3");
        else
            out.clazz = static_cast<int>(p["class"].get<int64_t>());
    }

    out.svar = rule.sdefault;
    out.evar = rule.edefault;
    if (p.contains("staticVariation"))
    {
        if (!is_integer(p["staticVariation"]) || !rule.svars.count(static_cast<int>(p["staticVariation"].get<int64_t>())))
            c.add(where + ": staticVariation is not one of the allowed variations");
        else
            out.svar = static_cast<int>(p["staticVariation"].get<int64_t>());
    }
    if (p.contains("eventVariation"))
    {
        if (!is_integer(p["eventVariation"]) || !rule.evars.count(static_cast<int>(p["eventVariation"].get<int64_t>())))
            c.add(where + ": eventVariation is not one of the allowed variations");
        else
            out.evar = static_cast<int>(p["eventVariation"].get<int64_t>());
    }

    if (p.contains("flags"))
    {
        out.flags = 0;
        const auto& flags = p["flags"];
        if (!flags.is_array())
        {
            c.add(where + ": flags must be an array");
        }
        else
        {
            for (const auto& f : flags)
            {
                const int bit = f.is_string() ? flag_bit(type, f.get<std::string>()) : -1;
                if (bit < 0)
                    c.add(where + ": flag " + f.dump() + " is not allowed for " + type);
                else if (out.flags & (1u << bit))
                    c.add(where + ": flag " + f.dump() + " is repeated");
                else
                    out.flags |= static_cast<uint8_t>(1u << bit);
            }
        }
    }

    // A static variation without flags reports the point ONLINE.
    const bool noFlags = (type == "binaryInputs" && out.svar == 1) || (type == "counters" && (out.svar == 5 || out.svar == 6))
        || (type == "frozenCounters" && (out.svar == 9 || out.svar == 10))
        || (type == "analogInputs" && (out.svar == 3 || out.svar == 4));
    if (noFlags && out.flags != 0x01)
        c.add(where + ": static variation " + std::to_string(out.svar) + " has no flags, so flags must be [\"ONLINE\"]");

    if (p.contains("selectRequired"))
    {
        if (!p["selectRequired"].is_boolean())
            c.add(where + ": selectRequired must be a boolean");
        else
            out.selectRequired = p["selectRequired"].get<bool>();
    }
    if (p.contains("target"))
    {
        if (!is_integer(p["target"]) || p["target"].get<int64_t>() < 0 || p["target"].get<int64_t>() > 65535)
            c.add(where + ": target must be an index");
        else
            out.target = static_cast<uint16_t>(p["target"].get<int64_t>());
    }
    for (const char* bound : {"min", "max"})
    {
        if (!p.contains(bound))
            continue;
        if (!p[bound].is_number())
        {
            c.add(where + ": " + bound + " must be a number");
            continue;
        }
        const double v = p[bound].get<double>();
        if (!is_float32(v))
            c.add(where + ": " + bound + " is not exactly representable in single precision");
        (std::string(bound) == "min" ? out.min : out.max) = v;
    }

    if (!p.contains("value"))
    {
        c.add(where + ": value is required");
        return;
    }
    const auto& v = p["value"];
    if (type == "binaryInputs" || type == "binaryOutputs")
    {
        if (!v.is_boolean())
            c.add(where + ": value must be a boolean");
        else
            out.value = v.get<bool>() ? 1 : 0;
        return;
    }
    if (type == "doubleBitInputs")
    {
        if (!is_integer(v) || v.get<int64_t>() < 0 || v.get<int64_t>() > 3)
            c.add(where + ": value must be 0..3");
        else
            out.value = static_cast<double>(v.get<int64_t>());
        return;
    }
    if (type == "octetStrings")
    {
        static const std::regex hex("^([0-9a-f]{2}){1,255}$");
        if (!v.is_string() || !std::regex_match(v.get<std::string>(), hex))
        {
            c.add(where + ": value must be 1..255 octets in lower-case hex");
            return;
        }
        const auto s = v.get<std::string>();
        for (size_t i = 0; i < s.size(); i += 2)
            out.octets.push_back(static_cast<uint8_t>(std::stoi(s.substr(i, 2), nullptr, 16)));
        return;
    }
    if (type == "counters" || type == "frozenCounters")
    {
        if (!is_integer(v) || v.get<int64_t>() < 0 || v.get<int64_t>() > 4294967295LL)
        {
            c.add(where + ": value must be an integer in 0..4294967295");
            return;
        }
        out.value = static_cast<double>(v.get<int64_t>());
        const std::set<int> s16 = type == "counters" ? std::set<int>{2, 6} : std::set<int>{2, 6, 10};
        const bool sixteen = s16.count(out.svar) || (out.clazz && (out.evar == 2 || out.evar == 6));
        if (sixteen && out.value > 65535)
            c.add(where + ": value does not fit a 16-bit variation");
        return;
    }
    // analog inputs and outputs
    if (!v.is_number())
    {
        c.add(where + ": value must be a number");
        return;
    }
    out.value = v.get<double>();
    const bool input = type == "analogInputs";
    const std::set<int> sint = input ? std::set<int>{1, 2, 3, 4} : std::set<int>{1, 2};
    const std::set<int> s16 = input ? std::set<int>{2, 4} : std::set<int>{2};
    const std::set<int> eint = {1, 2, 3, 4};
    const std::set<int> e16 = {2, 4};
    if ((sint.count(out.svar) || (out.clazz && eint.count(out.evar))) && out.value != std::floor(out.value))
        c.add(where + ": value is not an integer, but a variation of the point is");
    if (s16.count(out.svar) || (out.clazz && e16.count(out.evar)))
    {
        if (out.value < -32768 || out.value > 32767)
            c.add(where + ": value does not fit a 16-bit variation");
    }
    else if (out.value < -2147483648.0 || out.value > 2147483647.0)
    {
        c.add(where + ": value does not fit a 32-bit variation");
    }
    if (!is_float32(out.value))
        c.add(where + ": value is not exactly representable in single precision");
    if (out.min && out.max && *out.min > *out.max)
        c.add(where + ": min is larger than max");
    if ((out.min && out.value < *out.min) || (out.max && out.value > *out.max))
        c.add(where + ": value is outside [min, max]");
}

bool link_address(const json& v, uint16_t& out)
{
    if (!is_integer(v) || v.get<int64_t>() < 0 || v.get<int64_t>() > 65519)
        return false;
    out = static_cast<uint16_t>(v.get<int64_t>());
    return true;
}

} // namespace

const Point* Fixture::find(const std::string& type, uint16_t index) const
{
    auto it = points.find(type);
    if (it == points.end())
        return nullptr;
    for (const auto& p : it->second)
    {
        if (p.index == index)
            return &p;
    }
    return nullptr;
}

std::string load_fixture(const std::string& path, Fixture& out)
{
    std::ifstream in(path, std::ios::binary);
    if (!in)
        return "cannot read " + path;
    std::stringstream buffer;
    buffer << in.rdbuf();
    out.text = buffer.str();

    json fx;
    try
    {
        fx = json::parse(out.text);
    }
    catch (const std::exception& e)
    {
        return std::string("not JSON: ") + e.what();
    }
    if (!fx.is_object())
        return "not a JSON object";

    Checker c;
    std::set<std::string> top = {"schemaVersion", "name", "description", "outstation"};
    for (const auto& r : type_rules())
        top.insert(r.name);
    for (auto it = fx.begin(); it != fx.end(); ++it)
    {
        if (!top.count(it.key()))
            c.add("unknown field '" + it.key() + "'");
    }
    if (fx.value("schemaVersion", json()) != "1.0")
        c.add("schemaVersion must be \"1.0\"");
    static const std::regex name_rx("^[a-z0-9][a-z0-9-]*$");
    if (!fx.contains("name") || !fx["name"].is_string() || !std::regex_match(fx["name"].get<std::string>(), name_rx))
        c.add("name is missing or invalid");
    else
        out.name = fx["name"].get<std::string>();
    if (fx.contains("description") && !fx["description"].is_string())
        c.add("description must be a string");

    if (!fx.contains("outstation") || !fx["outstation"].is_object())
    {
        c.add("outstation is required");
    }
    else
    {
        const auto& o = fx["outstation"];
        const std::set<std::string> fields = {"address",           "masterAddress",      "unsolicited",
                                              "needTime",          "coldRestartDelayMs", "warmRestartDelayMs",
                                              "maxControlsPerRequest", "eventBufferSize"};
        for (auto it = o.begin(); it != o.end(); ++it)
        {
            if (!fields.count(it.key()))
                c.add("outstation: unknown field '" + it.key() + "'");
        }
        if (!o.contains("address") || !link_address(o["address"], out.address))
            c.add("outstation: address must be 0..65519");
        if (!o.contains("masterAddress") || !link_address(o["masterAddress"], out.masterAddress))
            c.add("outstation: masterAddress must be 0..65519");
        else if (out.address == out.masterAddress)
            c.add("outstation: address and masterAddress are equal");
        for (const char* b : {"unsolicited", "needTime"})
        {
            if (!o.contains(b))
                continue;
            if (!o[b].is_boolean())
                c.add(std::string("outstation: ") + b + " must be a boolean");
            else
                (std::string(b) == "unsolicited" ? out.unsolicited : out.needTime) = o[b].get<bool>();
        }
        if (o.contains("maxControlsPerRequest"))
        {
            const auto& m = o["maxControlsPerRequest"];
            if (!is_integer(m) || m.get<int64_t>() < 1 || m.get<int64_t>() > 255)
                c.add("outstation: maxControlsPerRequest must be 1..255");
            else
                out.maxControlsPerRequest = static_cast<uint16_t>(m.get<int64_t>());
        }
        if (o.contains("eventBufferSize"))
        {
            const auto& m = o["eventBufferSize"];
            if (!is_integer(m) || m.get<int64_t>() < 1 || m.get<int64_t>() > 65535)
                c.add("outstation: eventBufferSize must be 1..65535");
            else
                out.eventBufferSize = static_cast<uint16_t>(m.get<int64_t>());
        }
        for (const char* d : {"coldRestartDelayMs", "warmRestartDelayMs"})
        {
            if (!o.contains(d))
                continue;
            if (!is_integer(o[d]) || o[d].get<int64_t>() < 0 || o[d].get<int64_t>() > 65535)
                c.add(std::string("outstation: ") + d + " must be 0..65535");
            else
                (std::string(d) == "coldRestartDelayMs" ? out.coldRestartDelayMs : out.warmRestartDelayMs)
                    = static_cast<uint16_t>(o[d].get<int64_t>());
        }
    }

    for (const auto& rule : type_rules())
    {
        if (!fx.contains(rule.name))
            continue;
        const auto& list = fx[rule.name];
        if (!list.is_array())
        {
            c.add(std::string(rule.name) + " must be an array");
            continue;
        }
        auto& points = out.points[rule.name];
        std::set<uint16_t> seen;
        for (size_t i = 0; i < list.size(); ++i)
        {
            Point p;
            check_point(rule, list[i], i, p, c);
            if (seen.count(p.index))
                c.add(std::string(rule.name) + "[" + std::to_string(p.index) + "]: duplicate index");
            seen.insert(p.index);
            points.push_back(p);
        }
    }

    for (const auto& pair : {std::make_pair("binaryOutputs", "binaryInputs"), std::make_pair("analogOutputs", "analogInputs")})
    {
        std::map<uint16_t, uint16_t> targets;
        for (const auto& p : out.points[pair.first])
        {
            if (!p.target)
                continue;
            const std::string where = std::string(pair.first) + "[" + std::to_string(p.index) + "]";
            if (!out.find(pair.second, *p.target))
                c.add(where + ": target " + std::to_string(*p.target) + " is not one of the " + pair.second);
            else if (targets.count(*p.target))
                c.add(where + ": target " + std::to_string(*p.target) + " is also the target of output "
                      + std::to_string(targets[*p.target]));
            targets[*p.target] = p.index;
        }
    }
    for (const auto& p : out.points["frozenCounters"])
    {
        if (!out.find("counters", p.index))
            c.add("frozenCounters[" + std::to_string(p.index) + "]: there is no counter with this index to freeze");
    }

    if (c.errors.empty())
        return "";
    std::string message;
    for (const auto& e : c.errors)
        message += (message.empty() ? "" : "; ") + e;
    return message;
}

} // namespace adapter
