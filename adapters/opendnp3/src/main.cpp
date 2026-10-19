// SPDX-License-Identifier: MIT
//
// dnp3-interop adapter for opendnp3. Implements docs/CONTAINER_CONTRACT.md.

#include "common.h"

#include <cstdio>
#include <fstream>
#include <iostream>
#include <sstream>

#ifndef ADAPTER_VERSION
#define ADAPTER_VERSION "dev"
#endif
#ifndef UPSTREAM_VERSION
#define UPSTREAM_VERSION "unknown"
#endif
#ifndef UPSTREAM_REVISION
#define UPSTREAM_REVISION "unknown"
#endif

namespace
{

const char* kUsage = R"(usage: dnp3-interop <command>

  server [--fixture PATH] [--bind-address ADDR] [--port N] [--ready-file PATH]
         [--select-timeout-ms N] [--confirm-timeout-ms N] [LINK]
  client <operation> --host HOST [--port N] [--connect-timeout-ms N] [--timeout-ms N]
         [--collect-ms N] [LINK] [operation flags]
      integrity-poll
      event-poll           [--classes 1,2,3]
      read                 --group G --variation V [--start N --stop N]
      operate              --type g12v1|g41v1..4 --index N [--mode direct|sbo]
                           g12v1: --code LATCH_ON|LATCH_OFF|PULSE_ON|PULSE_OFF|CLOSE|TRIP
                                  [--count N] [--on-ms N] [--off-ms N]
                           g41:   --value V
      time-sync            [--procedure lan|non-lan]
      restart              [--type cold|warm]
      enable-unsolicited   [--classes 1,2,3]
      disable-unsolicited  [--classes 1,2,3]
      assign-class         --class C --group G [--start N --stop N]
      freeze               [--clear]
      monitor              [--duration-ms N]
  print-capabilities
  print-fixture [NAME]
  version

  LINK: [--local-addr N] [--remote-addr N]

See docs/CONTAINER_CONTRACT.md in https://github.com/otfabric/dnp3-interop
)";

int print_capabilities()
{
    using adapter::json;
    adapter::emit(json{
        {"schemaVersion", adapter::kSchemaVersion},
        {"adapter", adapter::kAdapter},
        {"adapterVersion", ADAPTER_VERSION},
        {"protocol", "dnp3"},
        {"upstream",
         {{"name", "opendnp3"},
          {"version", UPSTREAM_VERSION},
          {"revision", UPSTREAM_REVISION},
          {"license", "Apache-2.0"},
          {"language", "C++"},
          {"independentEngine", true}}},
        {"roles", {{"server", true}, {"client", true}}},
        {"features",
         {{"integrityPoll", true},
          {"eventPoll", true},
          {"read", true},
          {"readAllObjectsClient", true},
          {"directOperate", true},
          {"selectBeforeOperate", true},
          // The outstation sets IIN2.2 with every NOT_SUPPORTED status.
          {"parameterErrorWithNotSupported", true},
          {"directOperateNoAckServer", true},
          // The master has no API for it.
          {"directOperateNoAckClient", false},
          {"timeSyncLan", true},
          {"timeSyncNonLan", true},
          // The master synchronizes only when the outstation sets NEED_TIME.
          {"timeSyncOnDemand", false},
          {"restart", true},
          {"restartDelayClient", true},
          {"unsolicited", true},
          {"assignClassServer", true},
          {"assignClassClient", true},
          {"freeze", true},
          {"freezeNoAckServer", true},
          // The master always waits for a response to a freeze.
          {"freezeNoAckClient", false},
          {"octetStrings", true},
          {"tcp", true},
          {"udp", false},
          {"tls", false},
          {"serial", false},
          // opendnp3 3.x has neither.
          {"fileTransfer", false},
          {"secureAuthentication", false}}},
        {"pointTypes",
         {"binaryInputs", "doubleBitInputs", "binaryOutputs", "counters", "frozenCounters", "analogInputs",
          "analogOutputs", "octetStrings"}},
        {"commandTypes", {"g12v1", "g41v1", "g41v2", "g41v3", "g41v4"}},
        {"clientOperations",
         {"integrity-poll", "event-poll", "read", "operate", "time-sync", "restart", "enable-unsolicited",
          "disable-unsolicited", "assign-class", "freeze", "monitor"}},
    });
    return adapter::kExitOK;
}

int print_fixture(const std::string& name)
{
    if (name.empty() || name.find('/') != std::string::npos || name.find("..") != std::string::npos)
    {
        adapter::logf("print-fixture: '%s' is not a fixture name", name.c_str());
        return adapter::kExitUsage;
    }
    std::ifstream in("/fixtures/" + name + "/fixture.json", std::ios::binary);
    if (!in)
    {
        adapter::logf("print-fixture: no fixture '%s'", name.c_str());
        return adapter::kExitFailed;
    }
    std::stringstream buffer;
    buffer << in.rdbuf();
    const auto text = buffer.str();
    std::fwrite(text.data(), 1, text.size(), stdout);
    return adapter::kExitOK;
}

} // namespace

int main(int argc, char* argv[])
{
    std::vector<std::string> args(argv + 1, argv + argc);
    const std::string command = args.empty() ? "server" : args[0];
    const std::vector<std::string> rest = args.empty() ? std::vector<std::string>{}
                                                       : std::vector<std::string>(args.begin() + 1, args.end());
    try
    {
        if (command == "server")
            return adapter::run_server(rest);
        if (command == "client")
            return adapter::run_client(rest);
        if (command == "print-capabilities" && rest.empty())
            return print_capabilities();
        if (command == "print-fixture" && rest.size() <= 1)
            return print_fixture(rest.empty() ? "baseline" : rest[0]);
        if (command == "version" && rest.empty())
        {
            std::printf("dnp3-interop %s %s (opendnp3 %s, %s)\n", adapter::kAdapter, ADAPTER_VERSION, UPSTREAM_VERSION,
                        UPSTREAM_REVISION);
            return adapter::kExitOK;
        }
        if (command == "help" || command == "--help" || command == "-h")
        {
            std::fputs(kUsage, stdout);
            return adapter::kExitOK;
        }
    }
    catch (const std::exception& e)
    {
        adapter::logf("%s: %s", command.c_str(), e.what());
        return adapter::kExitFailed;
    }
    std::fputs(kUsage, stderr);
    return adapter::kExitUsage;
}
