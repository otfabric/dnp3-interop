// SPDX-License-Identifier: MIT
//
// The outstation: serves a fixture as docs/FIXTURES.md describes.

#include "common.h"
#include "stack.h"
#include "transport.h"

#include <opendnp3/DNP3Manager.h>
#include <opendnp3/outstation/ICommandHandler.h>
#include <opendnp3/outstation/IOutstationApplication.h>
#include <opendnp3/outstation/OutstationStackConfig.h>
#include <opendnp3/outstation/UpdateBuilder.h>

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <mutex>
#include <csignal>
#include <cstdio>
#include <fstream>
#include <unistd.h>

using namespace opendnp3;

namespace adapter
{

namespace
{

DNPTime dnp_now()
{
    return DNPTime(now_ms(), TimestampQuality::SYNCHRONIZED);
}

template<class Enum> Enum pick(int variation, std::initializer_list<std::pair<int, Enum>> table)
{
    for (const auto& entry : table)
    {
        if (entry.first == variation)
            return entry.second;
    }
    return table.begin()->second; // unreachable: the fixture was validated
}

PointClass point_class(int clazz)
{
    switch (clazz)
    {
    case 1:
        return PointClass::Class1;
    case 2:
        return PointClass::Class2;
    case 3:
        return PointClass::Class3;
    default:
        return PointClass::Class0;
    }
}

int class_number(PointClass pc)
{
    switch (pc)
    {
    case PointClass::Class1:
        return 1;
    case PointClass::Class2:
        return 2;
    case PointClass::Class3:
        return 3;
    default:
        return 0;
    }
}

const char* point_type_name(AssignClassType type)
{
    switch (type)
    {
    case AssignClassType::BinaryInput:
        return "binaryInputs";
    case AssignClassType::DoubleBinaryInput:
        return "doubleBitInputs";
    case AssignClassType::Counter:
        return "counters";
    case AssignClassType::FrozenCounter:
        return "frozenCounters";
    case AssignClassType::AnalogInput:
        return "analogInputs";
    case AssignClassType::BinaryOutputStatus:
        return "binaryOutputs";
    case AssignClassType::AnalogOutputStatus:
        return "analogOutputs";
    }
    return "unknown";
}

DatabaseConfig database_config(const Fixture& fx)
{
    DatabaseConfig db;
    for (const auto& p : fx.points.at("binaryInputs"))
    {
        auto& c = db.binary_input[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = pick<StaticBinaryVariation>(
            p.svar, {{2, StaticBinaryVariation::Group1Var2}, {1, StaticBinaryVariation::Group1Var1}});
        c.evariation = pick<EventBinaryVariation>(
            p.evar, {{2, EventBinaryVariation::Group2Var2}, {1, EventBinaryVariation::Group2Var1}});
    }
    for (const auto& p : fx.points.at("doubleBitInputs"))
    {
        auto& c = db.double_binary[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = StaticDoubleBinaryVariation::Group3Var2;
        c.evariation = pick<EventDoubleBinaryVariation>(
            p.evar, {{2, EventDoubleBinaryVariation::Group4Var2}, {1, EventDoubleBinaryVariation::Group4Var1}});
    }
    for (const auto& p : fx.points.at("binaryOutputs"))
    {
        auto& c = db.binary_output_status[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = StaticBinaryOutputStatusVariation::Group10Var2;
        c.evariation = pick<EventBinaryOutputStatusVariation>(
            p.evar, {{2, EventBinaryOutputStatusVariation::Group11Var2}, {1, EventBinaryOutputStatusVariation::Group11Var1}});
    }
    for (const auto& p : fx.points.at("counters"))
    {
        auto& c = db.counter[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = pick<StaticCounterVariation>(p.svar, {{1, StaticCounterVariation::Group20Var1},
                                                              {2, StaticCounterVariation::Group20Var2},
                                                              {5, StaticCounterVariation::Group20Var5},
                                                              {6, StaticCounterVariation::Group20Var6}});
        c.evariation = pick<EventCounterVariation>(p.evar, {{1, EventCounterVariation::Group22Var1},
                                                             {2, EventCounterVariation::Group22Var2},
                                                             {5, EventCounterVariation::Group22Var5},
                                                             {6, EventCounterVariation::Group22Var6}});
    }
    for (const auto& p : fx.points.at("frozenCounters"))
    {
        auto& c = db.frozen_counter[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = pick<StaticFrozenCounterVariation>(p.svar, {{1, StaticFrozenCounterVariation::Group21Var1},
                                                                    {2, StaticFrozenCounterVariation::Group21Var2},
                                                                    {5, StaticFrozenCounterVariation::Group21Var5},
                                                                    {6, StaticFrozenCounterVariation::Group21Var6},
                                                                    {9, StaticFrozenCounterVariation::Group21Var9},
                                                                    {10, StaticFrozenCounterVariation::Group21Var10}});
        c.evariation = pick<EventFrozenCounterVariation>(p.evar, {{1, EventFrozenCounterVariation::Group23Var1},
                                                                   {2, EventFrozenCounterVariation::Group23Var2},
                                                                   {5, EventFrozenCounterVariation::Group23Var5},
                                                                   {6, EventFrozenCounterVariation::Group23Var6}});
    }
    for (const auto& p : fx.points.at("analogInputs"))
    {
        auto& c = db.analog_input[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = pick<StaticAnalogVariation>(p.svar, {{1, StaticAnalogVariation::Group30Var1},
                                                             {2, StaticAnalogVariation::Group30Var2},
                                                             {3, StaticAnalogVariation::Group30Var3},
                                                             {4, StaticAnalogVariation::Group30Var4},
                                                             {5, StaticAnalogVariation::Group30Var5},
                                                             {6, StaticAnalogVariation::Group30Var6}});
        c.evariation = pick<EventAnalogVariation>(p.evar, {{1, EventAnalogVariation::Group32Var1},
                                                            {2, EventAnalogVariation::Group32Var2},
                                                            {3, EventAnalogVariation::Group32Var3},
                                                            {4, EventAnalogVariation::Group32Var4},
                                                            {5, EventAnalogVariation::Group32Var5},
                                                            {6, EventAnalogVariation::Group32Var6},
                                                            {7, EventAnalogVariation::Group32Var7},
                                                            {8, EventAnalogVariation::Group32Var8}});
    }
    for (const auto& p : fx.points.at("analogOutputs"))
    {
        auto& c = db.analog_output_status[p.index];
        c.clazz = point_class(p.clazz);
        c.svariation = pick<StaticAnalogOutputStatusVariation>(p.svar,
                                                                {{1, StaticAnalogOutputStatusVariation::Group40Var1},
                                                                 {2, StaticAnalogOutputStatusVariation::Group40Var2},
                                                                 {3, StaticAnalogOutputStatusVariation::Group40Var3},
                                                                 {4, StaticAnalogOutputStatusVariation::Group40Var4}});
        c.evariation = pick<EventAnalogOutputStatusVariation>(p.evar,
                                                               {{1, EventAnalogOutputStatusVariation::Group42Var1},
                                                                {2, EventAnalogOutputStatusVariation::Group42Var2},
                                                                {3, EventAnalogOutputStatusVariation::Group42Var3},
                                                                {4, EventAnalogOutputStatusVariation::Group42Var4},
                                                                {5, EventAnalogOutputStatusVariation::Group42Var5},
                                                                {6, EventAnalogOutputStatusVariation::Group42Var6},
                                                                {7, EventAnalogOutputStatusVariation::Group42Var7},
                                                                {8, EventAnalogOutputStatusVariation::Group42Var8}});
    }
    for (const auto& p : fx.points.at("octetStrings"))
    {
        auto& c = db.octet_string[p.index];
        c.clazz = point_class(p.clazz);
    }
    return db;
}

Updates initial_values(const Fixture& fx)
{
    UpdateBuilder b;
    const auto time = dnp_now();
    // Suppress: the initial values are the baseline, not events.
    for (const auto& p : fx.points.at("binaryInputs"))
        b.Update(Binary(p.value != 0, Flags(p.flags), time), p.index, EventMode::Suppress);
    for (const auto& p : fx.points.at("doubleBitInputs"))
        b.Update(DoubleBitBinary(static_cast<DoubleBit>(static_cast<uint8_t>(p.value)), Flags(p.flags), time), p.index,
                 EventMode::Suppress);
    for (const auto& p : fx.points.at("binaryOutputs"))
        b.Update(BinaryOutputStatus(p.value != 0, Flags(p.flags), time), p.index, EventMode::Suppress);
    for (const auto& p : fx.points.at("counters"))
        b.Update(Counter(static_cast<uint32_t>(p.value), Flags(p.flags), time), p.index, EventMode::Suppress);
    // Frozen counters have no update of their own: a freeze sets them. Their
    // initial value is put in the counter of the same index, frozen, and the
    // counter set back, all without events.
    for (const auto& p : fx.points.at("frozenCounters"))
    {
        b.Update(Counter(static_cast<uint32_t>(p.value), Flags(p.flags), time), p.index, EventMode::Suppress);
        b.FreezeCounter(p.index, false, EventMode::Suppress);
        const auto* c = fx.find("counters", p.index);
        b.Update(Counter(static_cast<uint32_t>(c->value), Flags(c->flags), time), p.index, EventMode::Suppress);
    }
    for (const auto& p : fx.points.at("analogInputs"))
        b.Update(Analog(p.value, Flags(p.flags), time), p.index, EventMode::Suppress);
    for (const auto& p : fx.points.at("analogOutputs"))
        b.Update(AnalogOutputStatus(p.value, Flags(p.flags), time), p.index, EventMode::Suppress);
    for (const auto& p : fx.points.at("octetStrings"))
        b.Update(OctetString(Buffer(p.octets.data(), p.octets.size())), p.index, EventMode::Suppress);
    return b.Build();
}

class Application final : public IOutstationApplication
{
public:
    explicit Application(const Fixture& fx) : fx(fx), needTime(fx.needTime) {}

    bool SupportsWriteAbsoluteTime() override { return true; }

    bool WriteAbsoluteTime(const UTCTimestamp& timestamp) override
    {
        needTime = false;
        emit(json{{"event", "time-sync"}, {"time", format_time(timestamp.msSinceEpoch)}});
        return true;
    }

    bool SupportsAssignClass() override { return true; }

    void RecordClassAssignment(AssignClassType type, PointClass clazz, uint16_t start, uint16_t stop) override
    {
        emit(json{{"event", "assign-class"},
                  {"pointType", point_type_name(type)},
                  {"class", class_number(clazz)},
                  {"start", start},
                  {"stop", stop}});
    }

    ApplicationIIN GetApplicationIIN() const override
    {
        ApplicationIIN iin;
        iin.needTime = needTime;
        return iin;
    }

    RestartMode ColdRestartSupport() const override
    {
        return fx.coldRestartDelayMs ? RestartMode::SUPPORTED_DELAY_FINE : RestartMode::UNSUPPORTED;
    }

    RestartMode WarmRestartSupport() const override
    {
        return fx.warmRestartDelayMs ? RestartMode::SUPPORTED_DELAY_FINE : RestartMode::UNSUPPORTED;
    }

    uint16_t ColdRestart() override
    {
        emit(json{{"event", "restart"}, {"type", "cold"}});
        return fx.coldRestartDelayMs.value_or(0);
    }

    uint16_t WarmRestart() override
    {
        emit(json{{"event", "restart"}, {"type", "warm"}});
        return fx.warmRestartDelayMs.value_or(0);
    }

    DNPTime Now() override { return dnp_now(); }

private:
    const Fixture& fx;
    std::atomic<bool> needTime;
};

const char* function_name(OperateType type)
{
    switch (type)
    {
    case OperateType::SelectBeforeOperate:
        return "operate";
    case OperateType::DirectOperate:
        return "direct-operate";
    case OperateType::DirectOperateNoAck:
        return "direct-operate-no-ack";
    }
    return "operate";
}

class Commands final : public ICommandHandler
{
public:
    explicit Commands(const Fixture& fx) : fx(fx) {}

    void Begin() override {}
    void End() override {}

    CommandStatus Select(const ControlRelayOutputBlock& c, uint16_t index) override
    {
        bool value = false;
        auto status = check_crob(c, index, false, value);
        crob_event("select", c, index, status);
        return status;
    }

    CommandStatus Operate(const ControlRelayOutputBlock& c, uint16_t index, IUpdateHandler& db, OperateType type) override
    {
        bool value = false;
        auto status = check_crob(c, index, type != OperateType::SelectBeforeOperate, value);
        if (status == CommandStatus::SUCCESS)
        {
            const auto* p = fx.find("binaryOutputs", index);
            const auto time = dnp_now();
            db.Update(BinaryOutputStatus(value, Flags(p->flags), time), index, EventMode::Detect);
            if (p->target)
            {
                const auto* t = fx.find("binaryInputs", *p->target);
                db.Update(Binary(value, Flags(t->flags), time), *p->target, EventMode::Detect);
            }
        }
        crob_event(function_name(type), c, index, status);
        return status;
    }

    CommandStatus Select(const AnalogOutputInt16& c, uint16_t index) override { return select(c.value, index, "g41v2"); }
    CommandStatus Select(const AnalogOutputInt32& c, uint16_t index) override { return select(c.value, index, "g41v1"); }
    CommandStatus Select(const AnalogOutputFloat32& c, uint16_t index) override { return select(c.value, index, "g41v3"); }
    CommandStatus Select(const AnalogOutputDouble64& c, uint16_t index) override { return select(c.value, index, "g41v4"); }

    CommandStatus Operate(const AnalogOutputInt16& c, uint16_t index, IUpdateHandler& db, OperateType type) override
    {
        return operate(c.value, index, db, type, "g41v2");
    }
    CommandStatus Operate(const AnalogOutputInt32& c, uint16_t index, IUpdateHandler& db, OperateType type) override
    {
        return operate(c.value, index, db, type, "g41v1");
    }
    CommandStatus Operate(const AnalogOutputFloat32& c, uint16_t index, IUpdateHandler& db, OperateType type) override
    {
        return operate(c.value, index, db, type, "g41v3");
    }
    CommandStatus Operate(const AnalogOutputDouble64& c, uint16_t index, IUpdateHandler& db, OperateType type) override
    {
        return operate(c.value, index, db, type, "g41v4");
    }

private:
    CommandStatus check_crob(const ControlRelayOutputBlock& c, uint16_t index, bool direct, bool& value) const
    {
        const auto* p = fx.find("binaryOutputs", index);
        if (!p)
            return CommandStatus::NOT_SUPPORTED;
        switch (c.rawCode)
        {
        case 0x03: // LATCH_ON
        case 0x41: // PULSE_ON, CLOSE
            value = true;
            break;
        case 0x04: // LATCH_OFF
        case 0x81: // PULSE_ON, TRIP
            value = false;
            break;
        default:
            return CommandStatus::NOT_SUPPORTED;
        }
        if (direct && p->selectRequired)
            return CommandStatus::NOT_SUPPORTED;
        return CommandStatus::SUCCESS;
    }

    CommandStatus check_analog(double value, uint16_t index, bool direct) const
    {
        const auto* p = fx.find("analogOutputs", index);
        if (!p)
            return CommandStatus::NOT_SUPPORTED;
        if ((p->min && value < *p->min) || (p->max && value > *p->max))
            return CommandStatus::OUT_OF_RANGE;
        if (direct && p->selectRequired)
            return CommandStatus::NOT_SUPPORTED;
        return CommandStatus::SUCCESS;
    }

    CommandStatus select(double value, uint16_t index, const char* type)
    {
        auto status = check_analog(value, index, false);
        analog_event("select", type, value, index, status);
        return status;
    }

    CommandStatus operate(double value, uint16_t index, IUpdateHandler& db, OperateType op, const char* type)
    {
        auto status = check_analog(value, index, op != OperateType::SelectBeforeOperate);
        if (status == CommandStatus::SUCCESS)
        {
            const auto* p = fx.find("analogOutputs", index);
            const auto time = dnp_now();
            db.Update(AnalogOutputStatus(value, Flags(p->flags), time), index, EventMode::Detect);
            if (p->target)
            {
                const auto* t = fx.find("analogInputs", *p->target);
                db.Update(Analog(value, Flags(t->flags), time), *p->target, EventMode::Detect);
            }
        }
        analog_event(function_name(op), type, value, index, status);
        return status;
    }

    static void crob_event(const char* function, const ControlRelayOutputBlock& c, uint16_t index, CommandStatus status)
    {
        emit(json{{"event", "command"},
                  {"function", function},
                  {"type", "g12v1"},
                  {"index", index},
                  {"code", c.rawCode},
                  {"count", c.count},
                  {"onMs", c.onTimeMS},
                  {"offMs", c.offTimeMS},
                  {"status", command_status_name(CommandStatusSpec::to_type(status))}});
    }

    static void analog_event(const char* function, const char* type, double value, uint16_t index, CommandStatus status)
    {
        emit(json{{"event", "command"},
                  {"function", function},
                  {"type", type},
                  {"index", index},
                  {"value", value},
                  {"status", command_status_name(CommandStatusSpec::to_type(status))}});
    }

    const Fixture& fx;
};

// Reports the connections of a TCP or TLS channel as events, and says when
// a UDP or serial channel is open (bound, or the device opened).
class ConnectionListener final : public IChannelListener
{
public:
    explicit ConnectionListener(bool events) : events(events) {}

    void OnStateChange(ChannelState state) override
    {
        std::lock_guard<std::mutex> lock(mutex);
        const bool now_open = state == ChannelState::OPEN;
        if (events && now_open && !open)
            emit(json{{"event", "connection-opened"}});
        else if (events && !now_open && open)
            emit(json{{"event", "connection-closed"}});
        open = now_open;
        cv.notify_all();
    }

    bool wait_open(std::chrono::milliseconds timeout)
    {
        std::unique_lock<std::mutex> lock(mutex);
        return cv.wait_for(lock, timeout, [this] { return open; });
    }

private:
    const bool events;
    std::mutex mutex;
    std::condition_variable cv;
    bool open = false;
};

} // namespace

int run_server(const std::vector<std::string>& argv)
{
    std::string fixturePath, bindAddress, readyFile;
    int64_t port = 20000, selectTimeout = 5000, confirmTimeout = 5000, count = 1;
    std::optional<int64_t> localAddr, remoteAddr;
    Transport transport;
    try
    {
        std::set<std::string> flags = {"fixture",           "bind-address",       "port",      "ready-file",
                                       "select-timeout-ms", "confirm-timeout-ms", "local-addr", "remote-addr",
                                       "outstations"};
        flags.insert(kServerTransportFlags.begin(), kServerTransportFlags.end());
        Args args(argv, flags, {});
        transport = parse_transport(args, true);
        fixturePath = args.str("fixture", kDefaultFixture);
        bindAddress = args.str("bind-address", "0.0.0.0");
        readyFile = args.str("ready-file", kDefaultReadyFile);
        port = args.integer("port", 20000, 1, 65535);
        selectTimeout = args.integer("select-timeout-ms", 5000, 1, 3600000);
        confirmTimeout = args.integer("confirm-timeout-ms", 5000, 1, 3600000);
        count = args.integer("outstations", 1, 1, 16);
        if (args.has("local-addr"))
            localAddr = args.integer("local-addr", 0, 0, 65519);
        if (args.has("remote-addr"))
            remoteAddr = args.integer("remote-addr", 0, 0, 65519);
    }
    catch (const UsageError& e)
    {
        logf("server: %s", e.message.c_str());
        return kExitUsage;
    }

    Fixture fx;
    for (const char* type : {"binaryInputs", "doubleBitInputs", "binaryOutputs", "counters", "frozenCounters",
                             "analogInputs", "analogOutputs", "octetStrings"})
        fx.points[type];
    const auto error = load_fixture(fixturePath, fx);
    if (!error.empty())
    {
        logf("server: fixture %s: %s", fixturePath.c_str(), error.c_str());
        return kExitFixtureInvalid;
    }
    for (const char* type : {"binaryInputs", "doubleBitInputs", "binaryOutputs", "counters", "frozenCounters",
                             "analogInputs", "analogOutputs", "octetStrings"})
        fx.points[type];
    const uint16_t local = static_cast<uint16_t>(localAddr.value_or(fx.address));
    const uint16_t remote = static_cast<uint16_t>(remoteAddr.value_or(fx.masterAddress));
    if (local + count - 1 > 65519 || (remote >= local && remote <= local + count - 1))
    {
        logf("server: --outstations %lld from link address %u leaves the range or meets the master's",
             static_cast<long long>(count), local);
        return kExitUsage;
    }

    // Signals are taken by sigwait below; block them before any thread starts.
    sigset_t signals;
    sigemptyset(&signals);
    sigaddset(&signals, SIGTERM);
    sigaddset(&signals, SIGINT);
    pthread_sigmask(SIG_BLOCK, &signals, nullptr);

    int code = kExitOK;
    {
        DNP3Manager manager(1, stderr_log_handler());
        std::shared_ptr<IChannel> channel;
        std::string address;
        const bool stream = transport.kind == "tcp";
        auto listener = std::make_shared<ConnectionListener>(stream);
        // Unreadable TLS files end the process before ready, listening or
        // dialling (the library would read them only when a connection
        // comes, and a dialling outstation would never say).
        if (transport.tls)
        {
            const auto error = tls_files_error(transport);
            if (!error.empty())
            {
                logf("server: TLS: %s", error.c_str());
                return kExitFailed;
            }
        }
        try
        {
            channel = server_channel(manager, transport, bindAddress, static_cast<uint16_t>(port), listener, address);
        }
        catch (const std::exception& e)
        {
            logf("server: cannot open the %s channel on %s:%lld: %s", transport.name().c_str(), bindAddress.c_str(),
                 static_cast<long long>(port), e.what());
            return kExitFailed;
        }


        OutstationStackConfig config(database_config(fx));
        config.outstation.eventBufferConfig = EventBufferConfig::AllTypes(fx.eventBufferSize.value_or(1000));
        config.outstation.params.allowUnsolicited = fx.unsolicited;
        config.outstation.params.unsolClassMask = ClassField::None();
        config.outstation.params.selectTimeout = TimeDuration::Milliseconds(selectTimeout);
        if (fx.maxControlsPerRequest)
            config.outstation.params.maxControlsPerRequest = *fx.maxControlsPerRequest;
        config.outstation.params.solConfirmTimeout = TimeDuration::Milliseconds(confirmTimeout);
        config.outstation.params.unsolConfirmTimeout = TimeDuration::Milliseconds(confirmTimeout);
        config.link.LocalAddr = local;
        config.link.RemoteAddr = remote;
        config.link.KeepAliveTimeout = TimeDuration::Max();

        // --outstations N (contract 1.3): N outstations at consecutive link
        // addresses, each with its own copy of the fixture, on one channel.
        std::vector<std::shared_ptr<IOutstation>> outstations;
        for (int64_t i = 0; i < count; i++)
        {
            config.link.LocalAddr = static_cast<uint16_t>(local + i);
            auto outstation = channel->AddOutstation("outstation-" + std::to_string(local + i),
                                                     std::make_shared<Commands>(fx), std::make_shared<Application>(fx),
                                                     config);
            outstation->Apply(initial_values(fx));
            outstation->Enable();
            outstations.push_back(outstation);
        }

        // A TCP or TLS server listens once it exists; a UDP socket is bound
        // and a serial device open when the channel says so, which it does
        // once a stack on it is enabled.
        if (!stream && !listener->wait_open(std::chrono::seconds(5)))
        {
            logf("server: cannot open %s", address.c_str());
            return kExitFailed;
        }

        {
            std::ofstream ready(readyFile);
            if (!ready)
            {
                logf("server: cannot write the readiness file %s", readyFile.c_str());
                return kExitFailed;
            }
        }
        emit(json{{"event", "ready"},
                  {"adapter", kAdapter},
                  {"address", address},
                  {"fixture", fx.name},
                  {"linkAddress", local},
                  {"masterAddress", remote},
                  {"transport", transport.name()},
                  {"dial", !transport.connect.empty()}});

        int sig = 0;
        sigwait(&signals, &sig);
        logf("server: signal %d, stopping", sig);
        std::remove(readyFile.c_str());
        for (auto& outstation : outstations)
            outstation->Disable();
        channel->Shutdown();
        manager.Shutdown();
    }
    emit(json{{"event", "stopped"}});
    return code;
}

} // namespace adapter
