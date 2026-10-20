// SPDX-License-Identifier: MIT

#include "transport.h"

#include "stack.h"

#include <opendnp3/channel/TLSConfig.h>

using namespace opendnp3;

namespace adapter
{

const std::set<std::string> kServerTransportFlags
    = {"transport", "tls-cert",  "tls-key", "tls-ca",    "udp-peer", "serial-device",
       "baud",      "data-bits", "parity",  "stop-bits", "connect",  "tls-server-name"};
const std::set<std::string> kClientTransportFlags
    = {"transport",      "tls-ca",        "tls-cert", "tls-key",   "tls-server-name", "udp-local-port",
       "serial-device", "baud",          "data-bits", "parity",   "stop-bits",       "bind-address"};
const std::set<std::string> kClientTransportSwitches = {"tls", "listen"};

namespace
{
const ChannelRetry kRetry(TimeDuration::Milliseconds(100), TimeDuration::Seconds(1));

// HOST:PORT; throws UsageError.
IPEndpoint endpoint(const std::string& text, const std::string& flag)
{
    const auto colon = text.rfind(':');
    if (colon == std::string::npos || colon == 0 || colon + 1 == text.size())
        throw UsageError{"--" + flag + " must be HOST:PORT"};
    try
    {
        const auto port = std::stol(text.substr(colon + 1));
        if (port < 1 || port > 65535)
            throw UsageError{"--" + flag + " must be HOST:PORT"};
        return IPEndpoint(text.substr(0, colon), static_cast<uint16_t>(port));
    }
    catch (const std::logic_error&)
    {
        throw UsageError{"--" + flag + " must be HOST:PORT"};
    }
}

TLSConfig tls_config(const Transport& t)
{
    // The peer's certificate is checked against the CA; TLS 1.2 and 1.3.
    return TLSConfig(t.ca, t.cert, t.key, false, false, true, true);
}
} // namespace

Transport parse_transport(const Args& args, bool server)
{
    Transport t;
    t.kind = args.str("transport", "tcp");
    if (t.kind != "tcp" && t.kind != "udp" && t.kind != "serial")
        throw UsageError{"--transport must be tcp, udp or serial"};
    for (const char* n : {"serial-device", "baud", "data-bits", "parity", "stop-bits"})
        if (args.has(n) && t.kind != "serial")
            throw UsageError{std::string("--") + n + " needs --transport serial"};
    for (const char* n : {"udp-peer", "udp-local-port"})
        if (args.has(n) && t.kind != "udp")
            throw UsageError{std::string("--") + n + " needs --transport udp"};

    t.cert = args.str("tls-cert", "");
    t.key = args.str("tls-key", "");
    t.ca = args.str("tls-ca", "");
    if (server)
    {
        if (args.has("connect"))
        {
            t.connect = args.str("connect", "");
            endpoint(t.connect, "connect");
            for (const char* n : {"bind-address", "port"})
                if (args.has(n))
                    throw UsageError{std::string("--") + n + " does not go with --connect: the outstation dials"};
            if (t.kind != "tcp")
                throw UsageError{"--connect needs --transport tcp"};
        }
        else if (args.has("tls-server-name"))
            throw UsageError{"--tls-server-name needs --connect"};
        t.tls = args.has("tls-cert");
        if (!t.tls && (args.has("tls-key") || args.has("tls-ca")))
            throw UsageError{"--tls-key and --tls-ca need --tls-cert"};
        if (t.tls && (!args.has("tls-key") || !args.has("tls-ca")))
            throw UsageError{"--tls-cert needs --tls-key and --tls-ca: TLS is mutual"};
        if (t.kind == "udp")
        {
            // The library's UDP socket is connected to one peer.
            if (!args.has("udp-peer"))
                throw UsageError{"--transport udp needs --udp-peer: the library's UDP socket is connected to one peer"};
            t.udpPeer = args.str("udp-peer", "");
            endpoint(t.udpPeer, "udp-peer");
        }
    }
    else
    {
        t.tls = args.has("tls");
        t.listen = args.has("listen");
        if (t.listen && t.kind != "tcp")
            throw UsageError{"--listen needs --transport tcp"};
        if (t.listen && args.has("host"))
            throw UsageError{"--host does not go with --listen: the master waits for the outstation"};
        if (!t.listen && args.has("bind-address"))
            throw UsageError{"--bind-address needs --listen"};
        if (!t.tls
            && (args.has("tls-ca") || args.has("tls-cert") || args.has("tls-key") || args.has("tls-server-name")))
            throw UsageError{"the --tls-* flags need --tls"};
        if (t.tls && (!args.has("tls-ca") || !args.has("tls-cert") || !args.has("tls-key")))
            throw UsageError{"--tls needs --tls-ca, --tls-cert and --tls-key: TLS is mutual"};
        t.udpLocalPort = static_cast<uint16_t>(args.integer("udp-local-port", 20000, 0, 65535));
    }
    if (t.tls && t.kind != "tcp")
        throw UsageError{"TLS needs --transport tcp"};

    if (t.kind == "serial")
    {
        for (const char* n : {"host", "port", "bind-address"})
            if (args.has(n))
                throw UsageError{std::string("--") + n + " does not go with --transport serial"};
        t.serial.deviceName = args.required("serial-device");
        t.serial.baud = static_cast<int>(args.integer("baud", 9600, 50, 4000000));
        t.serial.dataBits = static_cast<int>(args.integer("data-bits", 8, 7, 8));
        const auto parity = args.str("parity", "none");
        if (parity == "none")
            t.serial.parity = Parity::None;
        else if (parity == "even")
            t.serial.parity = Parity::Even;
        else if (parity == "odd")
            t.serial.parity = Parity::Odd;
        else
            throw UsageError{"--parity must be none, even or odd"};
        const auto stop = args.str("stop-bits", "1");
        if (stop == "1")
            t.serial.stopBits = StopBits::One;
        else if (stop == "2")
            t.serial.stopBits = StopBits::Two;
        else
            throw UsageError{"--stop-bits must be 1 or 2"};
        t.serial.flowType = FlowControl::None;
        t.serial.asyncOpenDelay = TimeDuration::Milliseconds(0);
    }
    return t;
}

std::shared_ptr<IChannel> server_channel(DNP3Manager& manager,
                                         const Transport& t,
                                         const std::string& bind,
                                         uint16_t port,
                                         std::shared_ptr<IChannelListener> listener,
                                         std::string& address)
{
    address = bind + ":" + std::to_string(port);
    if (!t.connect.empty())
    {
        // The channel does not care which end dialled: the outstation's
        // stack sits on a client channel. The library checks the master's
        // certificate against the CA but not its name.
        address = t.connect;
        if (t.tls)
            return manager.AddTLSClient("server", log_levels(), kRetry, {endpoint(t.connect, "connect")}, "0.0.0.0",
                                        tls_config(t), listener);
        return manager.AddTCPClient("server", log_levels(), kRetry, {endpoint(t.connect, "connect")}, "0.0.0.0",
                                    listener);
    }
    if (t.kind == "serial")
    {
        address = t.serial.deviceName;
        return manager.AddSerial("server", log_levels(), kRetry, t.serial, listener);
    }
    if (t.kind == "udp")
        return manager.AddUDPChannel("server", log_levels(), kRetry, IPEndpoint(bind, port),
                                     endpoint(t.udpPeer, "udp-peer"), listener);
    if (t.tls)
        return manager.AddTLSServer("server", log_levels(), ServerAcceptMode::CloseExisting, IPEndpoint(bind, port),
                                    tls_config(t), listener);
    return manager.AddTCPServer("server", log_levels(), ServerAcceptMode::CloseExisting, IPEndpoint(bind, port),
                                listener);
}

std::shared_ptr<IChannel> client_channel(DNP3Manager& manager,
                                         const Transport& t,
                                         const std::string& host,
                                         uint16_t port,
                                         std::shared_ptr<IChannelListener> listener,
                                         std::string& address)
{
    address = host + ":" + std::to_string(port);
    if (t.listen)
    {
        // The master's stack on a server channel: it waits for the outstation.
        if (t.tls)
            return manager.AddTLSServer("client", log_levels(), ServerAcceptMode::CloseExisting, IPEndpoint(host, port),
                                        tls_config(t), listener);
        return manager.AddTCPServer("client", log_levels(), ServerAcceptMode::CloseExisting, IPEndpoint(host, port),
                                    listener);
    }
    if (t.kind == "serial")
    {
        address = t.serial.deviceName;
        return manager.AddSerial("client", log_levels(), kRetry, t.serial, listener);
    }
    if (t.kind == "udp")
        return manager.AddUDPChannel("client", log_levels(), kRetry, IPEndpoint("0.0.0.0", t.udpLocalPort),
                                     IPEndpoint(host, port), listener);
    if (t.tls)
        // The library checks the outstation's certificate against the CA but
        // not its name: --tls-server-name has nothing to act on.
        return manager.AddTLSClient("client", log_levels(), kRetry, {IPEndpoint(host, port)}, "0.0.0.0",
                                    tls_config(t), listener);
    return manager.AddTCPClient("client", log_levels(), kRetry, {IPEndpoint(host, port)}, "0.0.0.0", listener);
}

} // namespace adapter
