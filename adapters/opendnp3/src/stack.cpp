// SPDX-License-Identifier: MIT

#include "stack.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>

namespace adapter
{

namespace
{
std::function<void(const char*)> header_observer;

bool debug()
{
    const char* debug = std::getenv("DNP3_INTEROP_DEBUG");
    return debug && std::strcmp(debug, "1") == 0;
}

class StderrLogHandler final : public opendnp3::ILogHandler
{
public:
    void log(opendnp3::ModuleId /*module*/,
             const char* id,
             opendnp3::LogLevel level,
             char const* /*location*/,
             char const* message) override
    {
        if (level == opendnp3::flags::APP_HEADER_RX)
        {
            if (header_observer)
                header_observer(message);
            if (!debug())
                return;
        }
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
    if (debug())
        return opendnp3::levels::ALL_COMMS;
    return opendnp3::levels::NORMAL;
}

opendnp3::LogLevels server_log_levels()
{
    return log_levels() | opendnp3::flags::APP_HEADER_RX;
}

void observe_request_headers(std::function<void(const char* message)> observer)
{
    header_observer = std::move(observer);
}

} // namespace adapter
