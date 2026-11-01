// SPDX-License-Identifier: MIT
//
// opendnp3 plumbing shared by server and client.

#pragma once

#include <opendnp3/logging/ILogHandler.h>
#include <opendnp3/logging/LogLevels.h>

#include <functional>
#include <memory>

namespace adapter
{

// Library logging on stderr (opendnp3's ConsoleLogger writes to stdout).
std::shared_ptr<opendnp3::ILogHandler> stderr_log_handler();
// NORMAL (errors and warnings); everything down to the frames with
// DNP3_INTEROP_DEBUG=1 in the environment.
opendnp3::LogLevels log_levels();
// The levels of an outstation's channel: log_levels() and the headers of what
// it receives, which only the observer sees without DNP3_INTEROP_DEBUG=1.
opendnp3::LogLevels server_log_levels();
// Called with each application header the outstation received. The library
// tells the application of a freeze by no other means (contract 1.4).
void observe_request_headers(std::function<void(const char* message)> observer);

} // namespace adapter
