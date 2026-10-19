// SPDX-License-Identifier: MIT
//
// opendnp3 plumbing shared by server and client.

#pragma once

#include <opendnp3/logging/ILogHandler.h>
#include <opendnp3/logging/LogLevels.h>

#include <memory>

namespace adapter
{

// Library logging on stderr (opendnp3's ConsoleLogger writes to stdout).
std::shared_ptr<opendnp3::ILogHandler> stderr_log_handler();
// NORMAL (errors and warnings); everything down to the frames with
// DNP3_INTEROP_DEBUG=1 in the environment.
opendnp3::LogLevels log_levels();

} // namespace adapter
