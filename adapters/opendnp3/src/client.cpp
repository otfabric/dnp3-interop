// SPDX-License-Identifier: MIT
//
// The master: one bounded operation per process, one result document.

#include "common.h"
#include "stack.h"
#include "transport.h"

#include <opendnp3/DNP3Manager.h>
#include <opendnp3/master/IMasterApplication.h>
#include <opendnp3/master/ISOEHandler.h>
#include <opendnp3/master/ITaskCallback.h>
#include <opendnp3/master/MasterStackConfig.h>

#include <chrono>
#include <cmath>
#include <condition_variable>
#include <functional>
#include <mutex>
#include <thread>

using namespace opendnp3;
using namespace std::chrono;

namespace adapter
{

namespace
{

// A number with no fractional part is printed as an integer, so that every
// adapter prints the same text for it.
json number(double v)
{
    if (std::isfinite(v) && v == std::floor(v) && std::fabs(v) < 9007199254740992.0)
        return static_cast<int64_t>(v);
    return v;
}

const std::set<std::string> kOperations = {"integrity-poll", "event-poll",          "read",         "operate",
                                           "time-sync",      "restart",             "enable-unsolicited",
                                           "disable-unsolicited", "assign-class",   "freeze",       "monitor", "operate-multi"};

// Everything the stack's threads report, guarded by one mutex.
struct State
{
    std::mutex mutex;
    std::condition_variable cv;

    bool open = false;
    bool wasOpen = false;
    bool lost = false;

    // The operation's task.
    std::optional<TaskCompletion> done;
    std::optional<uint8_t> commandStatus;
    std::vector<uint8_t> commandStatuses; // operate-multi
    bool commandMismatch = false;
    std::optional<int64_t> restartDelayMs;

    // The IIN of the response that completed the task: the library reports it
    // right after the completion, from the same response.
    bool awaitIIN = false;
    std::optional<IINField> iin;
    std::optional<IINField> lastIIN;

    std::optional<TaskCompletion> timeSync;

    json objects = json::array();
    bool unsolicitedFragment = false;

    template<class Pred> bool wait(milliseconds timeout, Pred pred)
    {
        std::unique_lock<std::mutex> lock(mutex);
        return cv.wait_for(lock, timeout, pred);
    }

    void complete(TaskCompletion result)
    {
        std::lock_guard<std::mutex> lock(mutex);
        done = result;
        awaitIIN = true;
        cv.notify_all();
    }
};

class ChannelListener final : public IChannelListener
{
public:
    explicit ChannelListener(State& s) : s(s) {}
    void OnStateChange(ChannelState state) override
    {
        std::lock_guard<std::mutex> lock(s.mutex);
        const bool open = state == ChannelState::OPEN;
        if (open)
            s.wasOpen = true;
        else if (s.open)
            s.lost = true;
        s.open = open;
        s.cv.notify_all();
    }

private:
    State& s;
};

class Application final : public IMasterApplication
{
public:
    explicit Application(State& s) : s(s) {}

    void OnReceiveIIN(const IINField& iin) override
    {
        std::lock_guard<std::mutex> lock(s.mutex);
        s.lastIIN = iin;
        if (s.awaitIIN)
        {
            s.iin = iin;
            s.awaitIIN = false;
        }
        s.cv.notify_all();
    }

    void OnTaskComplete(const TaskInfo& info) override
    {
        if (info.type == MasterTaskType::LAN_TIME_SYNC || info.type == MasterTaskType::NON_LAN_TIME_SYNC)
        {
            std::lock_guard<std::mutex> lock(s.mutex);
            s.timeSync = info.result;
            s.cv.notify_all();
        }
    }

    UTCTimestamp Now() override { return UTCTimestamp(now_ms()); }

private:
    State& s;
};

class Callback final : public ITaskCallback
{
public:
    explicit Callback(State& s) : s(s) {}
    void OnStart() override {}
    void OnComplete(TaskCompletion result) override { s.complete(result); }
    void OnDestroyed() override {}

private:
    State& s;
};

class Handler final : public ISOEHandler
{
public:
    explicit Handler(State& s) : s(s) {}

    void BeginFragment(const ResponseInfo& info) override
    {
        std::lock_guard<std::mutex> lock(s.mutex);
        s.unsolicitedFragment = info.unsolicited;
    }
    void EndFragment(const ResponseInfo&) override {}

    void Process(const HeaderInfo& info, const ICollection<Indexed<Binary>>& values) override
    {
        each(info, values, [](const Binary& m) { return json(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<DoubleBitBinary>>& values) override
    {
        each(info, values, [](const DoubleBitBinary& m) { return json(static_cast<int>(m.value)); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<Analog>>& values) override
    {
        each(info, values, [](const Analog& m) { return number(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<Counter>>& values) override
    {
        each(info, values, [](const Counter& m) { return json(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<FrozenCounter>>& values) override
    {
        each(info, values, [](const FrozenCounter& m) { return json(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<BinaryOutputStatus>>& values) override
    {
        each(info, values, [](const BinaryOutputStatus& m) { return json(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<AnalogOutputStatus>>& values) override
    {
        each(info, values, [](const AnalogOutputStatus& m) { return number(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<OctetString>>& values) override
    {
        const auto gv = GroupVariationSpec::to_type(info.gv);
        std::lock_guard<std::mutex> lock(s.mutex);
        values.ForeachItem([&](const Indexed<OctetString>& x) {
            const auto buffer = x.value.ToBuffer();
            s.objects.push_back(json{{"group", gv >> 8},
                                     {"variation", buffer.length},
                                     {"index", x.index},
                                     {"value", to_hex(buffer.data, buffer.length)},
                                     {"unsolicited", s.unsolicitedFragment}});
        });
    }
    // g50v4 (contract 1.2): the interval as the value, its time and units.
    void Process(const HeaderInfo& info, const ICollection<Indexed<TimeAndInterval>>& values) override
    {
        const auto gv = GroupVariationSpec::to_type(info.gv);
        std::lock_guard<std::mutex> lock(s.mutex);
        values.ForeachItem([&](const Indexed<TimeAndInterval>& x) {
            s.objects.push_back(json{{"group", gv >> 8},
                                     {"variation", gv & 0xff},
                                     {"index", x.index},
                                     {"value", x.value.interval},
                                     {"time", format_time(x.value.time.value)},
                                     {"units", x.value.units},
                                     {"unsolicited", s.unsolicitedFragment}});
        });
    }
    // g13 and g43 (contract 1.2): the commanded state or value and the
    // status, without flags.
    void Process(const HeaderInfo& info, const ICollection<Indexed<BinaryCommandEvent>>& values) override
    {
        command_events(info, values, [](const BinaryCommandEvent& m) { return json(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<Indexed<AnalogCommandEvent>>& values) override
    {
        command_events(info, values, [](const AnalogCommandEvent& m) { return number(m.value); });
    }
    void Process(const HeaderInfo& info, const ICollection<DNPTime>&) override { ignored(info); }

private:
    template<class T, class F> void each(const HeaderInfo& info, const ICollection<Indexed<T>>& values, F value)
    {
        const auto gv = GroupVariationSpec::to_type(info.gv);
        const uint8_t group = static_cast<uint8_t>(gv >> 8);
        const bool timed = info.tsquality != TimestampQuality::INVALID;
        std::lock_guard<std::mutex> lock(s.mutex);
        values.ForeachItem([&](const Indexed<T>& x) {
            json o{{"group", group}, {"variation", gv & 0xff}, {"index", x.index}, {"value", value(x.value)},
                   {"flags", flag_names(group, x.value.flags.value)}};
            if (timed)
                o["time"] = format_time(x.value.time.value);
            o["unsolicited"] = s.unsolicitedFragment;
            s.objects.push_back(std::move(o));
        });
    }

    template<class T, class F>
    void command_events(const HeaderInfo& info, const ICollection<Indexed<T>>& values, F value)
    {
        const auto gv = GroupVariationSpec::to_type(info.gv);
        const bool timed = info.tsquality != TimestampQuality::INVALID;
        std::lock_guard<std::mutex> lock(s.mutex);
        values.ForeachItem([&](const Indexed<T>& x) {
            json o{{"group", gv >> 8},
                   {"variation", gv & 0xff},
                   {"index", x.index},
                   {"value", value(x.value)},
                   {"status", command_status_name(CommandStatusSpec::to_type(x.value.status))}};
            if (timed)
                o["time"] = format_time(x.value.time.value);
            o["unsolicited"] = s.unsolicitedFragment;
            s.objects.push_back(std::move(o));
        });
    }

    static void ignored(const HeaderInfo& info)
    {
        logf("ignoring objects of %s: not part of the contract", GroupVariationSpec::to_human_string(info.gv));
    }

    State& s;
};

// A control of operate-multi's --controls: "g12v1:INDEX:CODE" or
// "g41vN:INDEX:VALUE".
struct Control
{
    std::string type;
    uint16_t index = 0;
    uint8_t code = 0;
    double value = 0;
};

std::vector<Control> parse_controls(const std::string& text)
{
    std::vector<Control> out;
    size_t start = 0;
    while (start <= text.size())
    {
        const auto end = std::min(text.find(',', start), text.size());
        const auto item = text.substr(start, end - start);
        start = end + 1;
        const auto a = item.find(':'), b = item.find(':', a == std::string::npos ? a : a + 1);
        if (a == std::string::npos || b == std::string::npos || item.find(':', b + 1) != std::string::npos)
            throw UsageError{"--controls items are TYPE:INDEX:CODE or TYPE:INDEX:VALUE"};
        Control c;
        c.type = item.substr(0, a);
        const auto index = item.substr(a + 1, b - a - 1), arg = item.substr(b + 1);
        try
        {
            size_t used = 0;
            const auto i = std::stol(index, &used);
            if (used != index.size() || i < 0 || i > 65535)
                throw std::invalid_argument("index");
            c.index = static_cast<uint16_t>(i);
        }
        catch (const std::logic_error&)
        {
            throw UsageError{"--controls: '" + index + "' is not a point index"};
        }
        if (c.type == "g12v1")
        {
            static const std::map<std::string, uint8_t> codes
                = {{"LATCH_ON", 0x03}, {"LATCH_OFF", 0x04}, {"PULSE_ON", 0x01},
                   {"PULSE_OFF", 0x02}, {"CLOSE", 0x41},    {"TRIP", 0x81}};
            const auto found = codes.find(arg);
            if (found == codes.end())
                throw UsageError{"--controls: '" + arg + "' is not a control code"};
            c.code = found->second;
        }
        else
        {
            try
            {
                size_t used = 0;
                c.value = std::stod(arg, &used);
                if (used != arg.size() || !std::isfinite(c.value))
                    throw std::invalid_argument("value");
            }
            catch (const std::logic_error&)
            {
                throw UsageError{"--controls: '" + arg + "' is not a number"};
            }
            const bool integral = c.value == std::floor(c.value);
            const bool ok = (c.type == "g41v1" && integral && c.value >= -2147483648.0 && c.value <= 2147483647.0)
                || (c.type == "g41v2" && integral && c.value >= -32768 && c.value <= 32767)
                || (c.type == "g41v3" && std::fabs(c.value) <= 3.4028234663852886e38) || c.type == "g41v4";
            if (!ok)
                throw UsageError{"--controls: '" + item + "' is not a control type with a value in its range"};
        }
        out.push_back(c);
    }
    return out;
}

bool request_error(const IINField& iin)
{
    return iin.IsSet(IINBit::FUNC_NOT_SUPPORTED) || iin.IsSet(IINBit::OBJECT_UNKNOWN) || iin.IsSet(IINBit::PARAM_ERROR);
}

PointClass point_class(int c)
{
    static const PointClass classes[] = {PointClass::Class0, PointClass::Class1, PointClass::Class2, PointClass::Class3};
    return classes[c];
}

struct Outcome
{
    std::string code;
    std::string message;
};

} // namespace

int run_client(const std::vector<std::string>& argv)
{
    if (argv.empty())
    {
        logf("client: missing operation");
        return kExitUsage;
    }
    const std::string op = argv[0];
    if (!kOperations.count(op))
    {
        logf("client: unknown operation '%s'", op.c_str());
        return kExitUsage;
    }

    std::set<std::string> flags = {"host", "port", "local-addr", "remote-addr", "connect-timeout-ms", "timeout-ms",
                                   "collect-ms"};
    flags.insert(kClientTransportFlags.begin(), kClientTransportFlags.end());
    std::set<std::string> switches = kClientTransportSwitches;
    if (op == "event-poll" || op == "enable-unsolicited" || op == "disable-unsolicited")
        flags.insert("classes");
    else if (op == "read")
        flags.insert({"group", "variation", "start", "stop"});
    else if (op == "operate")
        flags.insert({"type", "index", "code", "count", "on-ms", "off-ms", "value", "mode"});
    else if (op == "operate-multi")
        flags.insert({"controls", "mode"});
    else if (op == "time-sync")
        flags.insert("procedure");
    else if (op == "restart")
        flags.insert("type");
    else if (op == "assign-class")
        flags.insert({"class", "group", "start", "stop"});
    else if (op == "freeze")
    {
        switches.insert({"clear", "no-ack"});
        flags.insert({"group", "at", "interval-ms"});
    }
    else if (op == "monitor")
        flags.insert("duration-ms");

    // Everything is parsed before anything is sent.
    std::string host;
    int64_t port, local, remote, connectTimeout, timeout, collect = 0, duration = 0;
    std::vector<Header> headers;
    std::function<void(IMaster&, State&, std::shared_ptr<ISOEHandler>)> start;
    std::string procedure;
    Transport transport;
    try
    {
        Args args(std::vector<std::string>(argv.begin() + 1, argv.end()), flags, switches);
        transport = parse_transport(args, false);
        if (transport.listen)
            host = args.str("bind-address", "0.0.0.0");
        else if (transport.kind != "serial")
            host = args.required("host");
        port = args.integer("port", 20000, 1, 65535);
        local = args.integer("local-addr", 1, 0, 65519);
        remote = args.integer("remote-addr", 1024, 0, 65519);
        connectTimeout = args.integer("connect-timeout-ms", 5000, 1, 3600000);
        timeout = args.integer("timeout-ms", 5000, 1, 3600000);
        collect = args.integer("collect-ms", 0, 0, 3600000);

        auto task = [](State& s) { return TaskConfig::With(std::make_shared<Callback>(s)); };

        if (op == "integrity-poll")
        {
            start = [task](IMaster& m, State& s, std::shared_ptr<ISOEHandler> soe) {
                m.ScanClasses(ClassField::AllClasses(), soe, task(s));
            };
        }
        else if (op == "event-poll")
        {
            bool c[4] = {false, false, false, false};
            for (int k : args.classes("classes", "1,2,3", 1))
                c[k] = true;
            ClassField field(false, c[1], c[2], c[3]);
            start = [task, field](IMaster& m, State& s, std::shared_ptr<ISOEHandler> soe) {
                m.ScanClasses(field, soe, task(s));
            };
        }
        else if (op == "read")
        {
            const auto g = static_cast<uint8_t>(args.integer("group", 0, 0, 255));
            if (!args.has("group"))
                args.required("group");
            const auto v = static_cast<uint8_t>(args.integer("variation", 0, 0, 255));
            if (!args.has("variation"))
                args.required("variation");
            if (args.has("start") != args.has("stop"))
                throw UsageError{"--start and --stop go together"};
            Header h = Header::AllObjects(g, v);
            if (args.has("start"))
            {
                const auto a = args.integer("start", 0, 0, 65535), b = args.integer("stop", 0, 0, 65535);
                if (a > b)
                    throw UsageError{"--start is larger than --stop"};
                h = b <= 255 ? Header::Range8(g, v, static_cast<uint8_t>(a), static_cast<uint8_t>(b))
                             : Header::Range16(g, v, static_cast<uint16_t>(a), static_cast<uint16_t>(b));
            }
            start = [task, h](IMaster& m, State& s, std::shared_ptr<ISOEHandler> soe) { m.Scan({h}, soe, task(s)); };
        }
        else if (op == "operate")
        {
            const auto type = args.required("type");
            const auto index = static_cast<uint16_t>(args.integer("index", 0, 0, 65535));
            if (!args.has("index"))
                args.required("index");
            const auto mode = args.str("mode", "direct");
            if (mode == "direct-no-ack")
                throw UsageError{"--mode direct-no-ack: opendnp3's master cannot send DIRECT OPERATE NO ACK"};
            if (mode != "direct" && mode != "sbo")
                throw UsageError{"--mode must be direct, sbo or direct-no-ack"};
            const bool sbo = mode == "sbo";
            auto callback = [](State& s) {
                return [&s](const ICommandTaskResult& r) {
                    {
                        std::lock_guard<std::mutex> lock(s.mutex);
                        r.ForeachItem([&](const CommandPointResult& p) {
                            if (s.commandStatus)
                                return;
                            const bool echoed = p.state == CommandPointState::SUCCESS
                                || p.state == CommandPointState::SELECT_FAIL
                                || p.state == CommandPointState::OPERATE_FAIL
                                || p.state == CommandPointState::SELECT_SUCCESS;
                            if (echoed)
                                s.commandStatus = CommandStatusSpec::to_type(p.status);
                            else if (p.state == CommandPointState::SELECT_MISMATCH)
                                s.commandMismatch = true;
                        });
                    }
                    s.complete(r.summary);
                };
            };
            auto submit = [sbo, index, callback](auto command) {
                return [=](IMaster& m, State& s, std::shared_ptr<ISOEHandler>) {
                    if (sbo)
                        m.SelectAndOperate(command, index, callback(s));
                    else
                        m.DirectOperate(command, index, callback(s));
                };
            };
            if (type == "g12v1")
            {
                const auto code = args.required("code");
                uint8_t raw;
                if (code == "LATCH_ON")
                    raw = 0x03;
                else if (code == "LATCH_OFF")
                    raw = 0x04;
                else if (code == "PULSE_ON")
                    raw = 0x01;
                else if (code == "PULSE_OFF")
                    raw = 0x02;
                else if (code == "CLOSE")
                    raw = 0x41;
                else if (code == "TRIP")
                    raw = 0x81;
                else
                    throw UsageError{"--code must be LATCH_ON, LATCH_OFF, PULSE_ON, PULSE_OFF, CLOSE or TRIP"};
                if (args.has("value"))
                    throw UsageError{"--value is for g41"};
                ControlRelayOutputBlock crob(raw, static_cast<uint8_t>(args.integer("count", 1, 0, 255)),
                                             static_cast<uint32_t>(args.integer("on-ms", 100, 0, 4294967295LL)),
                                             static_cast<uint32_t>(args.integer("off-ms", 100, 0, 4294967295LL)));
                start = submit(crob);
            }
            else if (type == "g41v1" || type == "g41v2" || type == "g41v3" || type == "g41v4")
            {
                if (args.has("code") || args.has("count") || args.has("on-ms") || args.has("off-ms"))
                    throw UsageError{"--code, --count, --on-ms and --off-ms are for g12v1"};
                const double value = args.number("value");
                auto integral = [&](double min, double max) {
                    if (value != std::floor(value) || value < min || value > max)
                        throw UsageError{"--value is out of the range of " + type};
                };
                if (type == "g41v1")
                {
                    integral(-2147483648.0, 2147483647.0);
                    start = submit(AnalogOutputInt32(static_cast<int32_t>(value)));
                }
                else if (type == "g41v2")
                {
                    integral(-32768, 32767);
                    start = submit(AnalogOutputInt16(static_cast<int16_t>(value)));
                }
                else if (type == "g41v3")
                {
                    if (std::fabs(value) > 3.4028234663852886e38)
                        throw UsageError{"--value is out of the range of g41v3"};
                    start = submit(AnalogOutputFloat32(static_cast<float>(value)));
                }
                else
                {
                    start = submit(AnalogOutputDouble64(value));
                }
            }
            else
            {
                throw UsageError{"--type must be g12v1, g41v1, g41v2, g41v3 or g41v4"};
            }
        }
        else if (op == "operate-multi")
        {
            const auto mode = args.str("mode", "direct");
            if (mode != "direct" && mode != "sbo")
                throw UsageError{"--mode must be direct or sbo"};
            const bool sbo = mode == "sbo";
            auto specs = std::make_shared<std::vector<Control>>(parse_controls(args.required("controls")));
            start = [sbo, specs](IMaster& m, State& s, std::shared_ptr<ISOEHandler>) {
                // One header per control, in the order given.
                CommandSet set;
                for (const auto& c : *specs)
                {
                    if (c.type == "g12v1")
                        set.StartHeader<ControlRelayOutputBlock>().Add(ControlRelayOutputBlock(c.code, 1, 100, 100), c.index);
                    else if (c.type == "g41v1")
                        set.StartHeader<AnalogOutputInt32>().Add(AnalogOutputInt32(static_cast<int32_t>(c.value)), c.index);
                    else if (c.type == "g41v2")
                        set.StartHeader<AnalogOutputInt16>().Add(AnalogOutputInt16(static_cast<int16_t>(c.value)), c.index);
                    else if (c.type == "g41v3")
                        set.StartHeader<AnalogOutputFloat32>().Add(AnalogOutputFloat32(static_cast<float>(c.value)), c.index);
                    else
                        set.StartHeader<AnalogOutputDouble64>().Add(AnalogOutputDouble64(c.value), c.index);
                }
                auto callback = [&s](const ICommandTaskResult& r) {
                    {
                        std::lock_guard<std::mutex> lock(s.mutex);
                        r.ForeachItem([&](const CommandPointResult& p) {
                            const bool echoed = p.state == CommandPointState::SUCCESS
                                || p.state == CommandPointState::SELECT_FAIL
                                || p.state == CommandPointState::OPERATE_FAIL
                                || p.state == CommandPointState::SELECT_SUCCESS;
                            if (!echoed)
                            {
                                s.commandMismatch = true;
                                return;
                            }
                            const auto st = CommandStatusSpec::to_type(p.status);
                            s.commandStatuses.push_back(st);
                            if (!s.commandStatus || (*s.commandStatus == 0 && st != 0))
                                s.commandStatus = st;
                        });
                    }
                    s.complete(r.summary);
                };
                if (sbo)
                    m.SelectAndOperate(std::move(set), callback);
                else
                    m.DirectOperate(std::move(set), callback);
            };
        }
        else if (op == "time-sync")
        {
            procedure = args.str("procedure", "lan");
            if (procedure != "lan" && procedure != "non-lan")
                throw UsageError{"--procedure must be lan or non-lan"};
            // The library synchronizes when the outstation asks for it: read
            // the IIN with a READ that has no object headers.
            start = [task](IMaster& m, State& s, std::shared_ptr<ISOEHandler> soe) { m.Scan({}, soe, task(s)); };
        }
        else if (op == "restart")
        {
            const auto type = args.str("type", "cold");
            if (type != "cold" && type != "warm")
                throw UsageError{"--type must be cold or warm"};
            const auto rt = type == "cold" ? RestartType::COLD : RestartType::WARM;
            start = [rt](IMaster& m, State& s, std::shared_ptr<ISOEHandler>) {
                m.Restart(rt, [&s](const RestartOperationResult& r) {
                    {
                        std::lock_guard<std::mutex> lock(s.mutex);
                        if (r.summary == TaskCompletion::SUCCESS)
                            s.restartDelayMs = duration_cast<milliseconds>(r.restartTime.value).count();
                    }
                    s.complete(r.summary);
                });
            };
        }
        else if (op == "enable-unsolicited" || op == "disable-unsolicited" || op == "assign-class" || op == "freeze")
        {
            FunctionCode fc;
            if (op == "enable-unsolicited" || op == "disable-unsolicited")
            {
                fc = op == "enable-unsolicited" ? FunctionCode::ENABLE_UNSOLICITED : FunctionCode::DISABLE_UNSOLICITED;
                for (int k : args.classes("classes", "1,2,3", 1))
                    headers.push_back(Header::From(point_class(k)));
            }
            else if (op == "assign-class")
            {
                fc = FunctionCode::ASSIGN_CLASS;
                const auto c = static_cast<int>(args.integer("class", 0, 0, 3));
                if (!args.has("class"))
                    args.required("class");
                const auto g = static_cast<uint8_t>(args.integer("group", 0, 0, 255));
                if (!args.has("group"))
                    args.required("group");
                if (args.has("start") != args.has("stop"))
                    throw UsageError{"--start and --stop go together"};
                headers.push_back(Header::From(point_class(c)));
                if (args.has("start"))
                {
                    const auto a = args.integer("start", 0, 0, 65535), b = args.integer("stop", 0, 0, 65535);
                    if (a > b)
                        throw UsageError{"--start is larger than --stop"};
                    headers.push_back(b <= 255 ? Header::Range8(g, 0, static_cast<uint8_t>(a), static_cast<uint8_t>(b))
                                               : Header::Range16(g, 0, static_cast<uint16_t>(a), static_cast<uint16_t>(b)));
                }
                else
                {
                    headers.push_back(Header::AllObjects(g, 0));
                }
            }
            else
            {
                if (args.has("no-ack"))
                    throw UsageError{"--no-ack: opendnp3's master cannot send a freeze without response"};
                if (args.has("at") || args.has("interval-ms"))
                    throw UsageError{"--at: opendnp3's master cannot send FREEZE AT TIME (its requests carry no g50v2)"};
                fc = args.has("clear") ? FunctionCode::FREEZE_CLEAR : FunctionCode::IMMED_FREEZE;
                // Counters, or analog inputs with --group 30 (contract 1.3).
                const auto group = args.integer("group", 20, 20, 30);
                if (group != 20 && group != 30)
                    throw UsageError{"--group must be 20 or 30"};
                headers.push_back(Header::AllObjects(static_cast<uint8_t>(group), 0));
            }
            start = [task, fc, headers, op](IMaster& m, State& s, std::shared_ptr<ISOEHandler>) {
                m.PerformFunction(op, fc, headers, task(s));
            };
        }
        else if (op == "monitor")
        {
            duration = args.integer("duration-ms", 1000, 0, 3600000);
        }
    }
    catch (const UsageError& e)
    {
        logf("client %s: %s", op.c_str(), e.message.c_str());
        return kExitUsage;
    }

    const auto started = steady_clock::now();
    State s;
    std::optional<Outcome> failure;
    bool connected = false;
    {
        DNP3Manager manager(1, stderr_log_handler());
        std::string address;
        auto channel = client_channel(manager, transport, host, static_cast<uint16_t>(port),
                                      std::make_shared<ChannelListener>(s), address);

        MasterStackConfig config;
        config.master.responseTimeout = TimeDuration::Milliseconds(timeout);
        config.master.taskStartTimeout = TimeDuration::Milliseconds(timeout);
        config.master.disableUnsolOnStartup = false;
        config.master.unsolClassMask = ClassField::None();
        config.master.startupIntegrityClassMask = ClassField::None();
        config.master.integrityOnEventOverflowIIN = false;
        config.master.eventScanOnEventsAvailableClassMask = ClassField::None();
        config.master.timeSyncMode = op != "time-sync" ? TimeSyncMode::None
            : procedure == "lan"                       ? TimeSyncMode::LAN
                                                       : TimeSyncMode::NonLAN;
        config.link.LocalAddr = static_cast<uint16_t>(local);
        config.link.RemoteAddr = static_cast<uint16_t>(remote);
        config.link.KeepAliveTimeout = TimeDuration::Max();

        auto soe = std::make_shared<Handler>(s);
        auto master = channel->AddMaster("master", soe, std::make_shared<Application>(s), config);
        master->Enable();

        connected = s.wait(milliseconds(connectTimeout), [&] { return s.open; });
        if (!connected)
        {
            failure = Outcome{"connect-failed", "no connection to " + address};
        }
        else if (op == "monitor")
        {
            s.wait(milliseconds(duration), [&] { return s.lost; });
        }
        else
        {
            start(*master, s, soe);
            // The library bounds every request by the response timeout; this
            // only guards against a task that never reports.
            const auto guard = milliseconds(3 * timeout + 2000);
            if (!s.wait(guard, [&] { return s.done.has_value(); }))
            {
                failure = Outcome{"timeout", "the operation did not complete"};
            }
            else
            {
                const auto result = *s.done;
                if (result == TaskCompletion::SUCCESS || result == TaskCompletion::FAILURE_BAD_RESPONSE)
                    s.wait(milliseconds(1000), [&] { return !s.awaitIIN; });
            }
            // Any response carrying NEED_TIME (the null unsolicited response
            // included) makes the library start the procedure; the READ only
            // makes sure one arrives. Wait for the procedure, and conclude that
            // the outstation does not need time only when none ran.
            if (!failure && op == "time-sync" && *s.done == TaskCompletion::SUCCESS)
            {
                const bool ran = s.wait(milliseconds(3 * timeout + 2000), [&] {
                    return s.timeSync.has_value() || (s.lastIIN && !s.lastIIN->IsSet(IINBit::NEED_TIME));
                });
                if (ran && s.timeSync)
                {
                    // The procedure decides, not the IIN of the probing READ
                    // (which an outstation may refuse: go-dnp3 rejects a READ
                    // without objects) or of whichever response came last.
                    std::lock_guard<std::mutex> l(s.mutex);
                    s.done = *s.timeSync;
                    if (*s.timeSync == TaskCompletion::SUCCESS)
                        s.iin.reset();
                    else
                        s.iin = s.lastIIN;
                }
                else
                {
                    std::lock_guard<std::mutex> l(s.mutex);
                    if (s.lastIIN && !s.lastIIN->IsSet(IINBit::NEED_TIME))
                        failure = Outcome{"failed", "the outstation does not need time; opendnp3 synchronizes only then"};
                    else
                        failure = Outcome{"timeout", "the time synchronization did not complete"};
                }
            }
        }

        if (!failure && op != "monitor")
        {
            std::lock_guard<std::mutex> lock(s.mutex);
            const auto result = *s.done;
            const bool rejected = s.iin && request_error(*s.iin);
            // A request error wins over the echo: not every library hands the
            // echo of a refused request to the application, so the contract
            // reports neither.
            if (rejected)
            {
                std::string message = "the outstation refused the request (IIN2)";
                if ((op == "operate" || op == "operate-multi") && s.commandStatus)
                    message += "; it echoed " + command_status_name(*s.commandStatus);
                s.commandStatus.reset();
                s.commandStatuses.clear();
                failure = Outcome{"rejected", message};
            }
            else if ((op == "operate" || op == "operate-multi") && s.commandStatus && *s.commandStatus != 0)
                failure = Outcome{"command-failed", "the outstation answered " + command_status_name(*s.commandStatus)};
            else if (s.lost || result == TaskCompletion::FAILURE_NO_COMMS)
                failure = Outcome{"connection-lost", "the connection closed during the operation"};
            else if (result == TaskCompletion::FAILURE_RESPONSE_TIMEOUT || result == TaskCompletion::FAILURE_START_TIMEOUT)
                failure = Outcome{"timeout", "no response from the outstation"};
            else if ((op == "operate" || op == "operate-multi") && (s.commandMismatch || !s.commandStatus))
                failure = Outcome{"failed", "the outstation's echo did not match the request"};
            else if (result != TaskCompletion::SUCCESS)
                failure = Outcome{"failed", std::string("task ") + TaskCompletionSpec::to_human_string(result)};
        }
        else if (!failure && op == "monitor")
        {
            std::lock_guard<std::mutex> lock(s.mutex);
            if (s.lost)
                failure = Outcome{"connection-lost", "the connection closed during the monitoring"};
        }

        if (!failure && collect > 0)
            std::this_thread::sleep_for(milliseconds(collect));

        // The library queues the application confirm of the last response and
        // writes it asynchronously; closing at once can lose it, and the
        // outstation then keeps events the master has already reported.
        if (connected)
            std::this_thread::sleep_for(milliseconds(200));

        master->Disable();
        channel->Shutdown();
        manager.Shutdown();
    }

    std::lock_guard<std::mutex> lock(s.mutex);
    json doc{{"schemaVersion", kSchemaVersion},
             {"adapter", kAdapter},
             {"operation", op},
             {"ok", !failure.has_value()},
             {"error", failure ? json{{"code", failure->code}, {"message", failure->message}} : json(nullptr)},
             {"connected", connected}};
    const bool reads = op == "integrity-poll" || op == "event-poll" || op == "read";
    doc["iin"] = reads && s.iin ? iin_names(s.iin->LSB, s.iin->MSB) : json(nullptr);
    doc["objects"] = s.objects;
    if (op == "operate" || op == "operate-multi")
        doc["commandStatus"] = s.commandStatus ? json(command_status_name(*s.commandStatus)) : json(nullptr);
    if (op == "operate-multi")
    {
        json statuses = json::array();
        for (auto st : s.commandStatuses)
            statuses.push_back(command_status_name(st));
        doc["commandStatuses"] = s.commandStatuses.empty() ? json(nullptr) : statuses;
    }
    if (op == "restart")
        doc["restartDelayMs"] = s.restartDelayMs ? json(*s.restartDelayMs) : json(nullptr);
    doc["elapsedMs"] = duration_cast<milliseconds>(steady_clock::now() - started).count();
    emit(doc);

    if (!failure)
        return kExitOK;
    return failure->code == "connect-failed" ? kExitConnectFailed : kExitFailed;
}

} // namespace adapter
