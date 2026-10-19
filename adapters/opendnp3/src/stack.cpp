// SPDX-License-Identifier: MIT

#include "stack.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>

namespace adapter
{

namespace
{
class StderrLogHandler final : public opendnp3::ILogHandler
{
public:
    void log(opendnp3::ModuleId /*module*/,
             const char* id,
             opendnp3::LogLevel level,
             char const* /*location*/,
             char const* message) override
    {
        std::fprintf(stderr, "[opendnp3] %s %s %s\n", opendnp3::LogFlagToString(level), id, message);
    }
};
} // namespace

std::shared_ptr<opendnp3::ILogHandler> stderr_log_handler()
{
    return std::make_shared<StderrLogHandler>();
}

opendnp3::LogLevels log_levels()
{
    const char* debug = std::getenv("DNP3_INTEROP_DEBUG");
    if (debug && std::strcmp(debug, "1") == 0)
        return opendnp3::levels::ALL_COMMS;
    return opendnp3::levels::NORMAL;
}

} // namespace adapter
