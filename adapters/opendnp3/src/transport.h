// SPDX-License-Identifier: MIT
//
// Transports (contract 1.1): TCP, TLS, UDP and serial, on the library's
// channels.

#pragma once

#include "common.h"

#include <opendnp3/DNP3Manager.h>

#include <memory>
#include <set>
#include <string>

namespace adapter
{

extern const std::set<std::string> kServerTransportFlags;
extern const std::set<std::string> kClientTransportFlags;
extern const std::set<std::string> kClientTransportSwitches;

struct Transport
{
    std::string kind = "tcp"; // tcp, udp or serial
    bool tls = false;
    std::string cert, key, ca;
    std::string udpPeer; // server: HOST:PORT of the master
    uint16_t udpLocalPort = 20000;
    opendnp3::SerialSettings serial;

    // What the ready event reports.
    std::string name() const { return tls ? "tls" : kind; }
};

// Parses the transport flags; throws UsageError.
Transport parse_transport(const Args& args, bool server);

// The outstation's channel. "address" is what the ready event reports.
// Throws std::exception when the channel cannot be opened.
std::shared_ptr<opendnp3::IChannel> server_channel(opendnp3::DNP3Manager& manager,
                                                   const Transport& t,
                                                   const std::string& bind,
                                                   uint16_t port,
                                                   std::shared_ptr<opendnp3::IChannelListener> listener,
                                                   std::string& address);

// The master's channel to host:port (or the serial device).
std::shared_ptr<opendnp3::IChannel> client_channel(opendnp3::DNP3Manager& manager,
                                                   const Transport& t,
                                                   const std::string& host,
                                                   uint16_t port,
                                                   std::shared_ptr<opendnp3::IChannelListener> listener,
                                                   std::string& address);

} // namespace adapter
