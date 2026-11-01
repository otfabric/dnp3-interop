// SPDX-License-Identifier: MIT
//
// The server's control socket and the counter-increment command (contract
// 1.4). The two talk one line of JSON each way over a Unix socket; that
// exchange is private to this adapter.

#include "common.h"

#include <cerrno>
#include <chrono>
#include <cstring>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/un.h>
#include <unistd.h>

namespace adapter
{

namespace
{

// Connect or bind address; false when the path does not fit.
bool socket_address(const std::string& path, sockaddr_un& address)
{
    std::memset(&address, 0, sizeof(address));
    address.sun_family = AF_UNIX;
    if (path.size() >= sizeof(address.sun_path))
        return false;
    std::strncpy(address.sun_path, path.c_str(), sizeof(address.sun_path) - 1);
    return true;
}

// Reads up to a newline, into line; false on an error or end of stream first.
bool read_line(int fd, std::string& line)
{
    char c = 0;
    while (true)
    {
        const auto n = read(fd, &c, 1);
        if (n <= 0)
            return false;
        if (c == '\n')
            return true;
        line.push_back(c);
    }
}

void write_all(int fd, const std::string& text)
{
    size_t done = 0;
    while (done < text.size())
    {
        const auto n = write(fd, text.data() + done, text.size() - done);
        if (n <= 0)
            return;
        done += static_cast<size_t>(n);
    }
}

void set_timeout(int fd, int64_t ms)
{
    timeval t{};
    t.tv_sec = static_cast<time_t>(ms / 1000);
    t.tv_usec = static_cast<suseconds_t>((ms % 1000) * 1000);
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &t, sizeof(t));
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &t, sizeof(t));
}

} // namespace

ControlSocket::ControlSocket(std::string path, CounterIncrement increment)
    : path(std::move(path)), increment(std::move(increment))
{
}

ControlSocket::~ControlSocket()
{
    if (listener >= 0)
    {
        shutdown(listener, SHUT_RDWR);
        if (thread.joinable())
            thread.join();
        close(listener);
        unlink(path.c_str());
    }
}

std::string ControlSocket::open()
{
    sockaddr_un address;
    if (!socket_address(path, address))
        return "the path is too long";
    unlink(path.c_str());
    listener = socket(AF_UNIX, SOCK_STREAM, 0);
    if (listener < 0)
        return std::strerror(errno);
    if (bind(listener, reinterpret_cast<sockaddr*>(&address), sizeof(address)) != 0 || listen(listener, 8) != 0)
    {
        const std::string problem = std::strerror(errno);
        close(listener);
        listener = -1;
        return problem;
    }
    thread = std::thread([this] { serve(); });
    return "";
}

void ControlSocket::serve()
{
    while (true)
    {
        const int fd = accept(listener, nullptr, nullptr);
        if (fd < 0)
            return;
        set_timeout(fd, 5000);
        std::string line;
        json reply{{"ok", false}, {"message", "unknown control operation"}};
        if (read_line(fd, line))
        {
            const auto request = json::parse(line, nullptr, false);
            if (!request.is_discarded() && request.value("operation", "") == "counter-increment"
                && request.contains("index") && request.contains("by"))
            {
                uint32_t value = 0;
                const auto problem
                    = increment(request["index"].get<uint16_t>(), request["by"].get<uint32_t>(), value);
                if (problem.empty())
                    reply = json{{"ok", true}, {"value", value}};
                else
                    reply = json{{"ok", false}, {"message", problem}};
            }
        }
        write_all(fd, reply.dump() + "\n");
        close(fd);
    }
}

int run_counter_increment(const std::vector<std::string>& argv)
{
    const auto start = std::chrono::steady_clock::now();
    int64_t index = 0, by = 1, timeout = 5000;
    std::string path;
    try
    {
        Args args(argv, {"index", "by", "control-socket", "timeout-ms"}, {});
        if (!args.has("index"))
            throw UsageError{"--index is required"};
        index = args.integer("index", 0, 0, 65535);
        by = args.integer("by", 1, 1, 4294967295);
        timeout = args.integer("timeout-ms", 5000, 1, 3600000);
        path = args.str("control-socket", kDefaultControlSocket);
    }
    catch (const UsageError& e)
    {
        logf("counter-increment: %s", e.message.c_str());
        return kExitUsage;
    }
    const auto finish = [&](bool connected, const json& value, const char* code, const std::string& message, int exit) {
        json doc{{"schemaVersion", kSchemaVersion}, {"adapter", kAdapter}, {"operation", "counter-increment"},
                 {"ok", code == nullptr}};
        doc["error"] = code ? json{{"code", code}, {"message", message}} : json(nullptr);
        doc["connected"] = connected;
        doc["iin"] = nullptr;
        doc["objects"] = json::array();
        doc["index"] = index;
        doc["value"] = value;
        doc["elapsedMs"] = std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() - start)
                               .count();
        emit(doc);
        return exit;
    };

    sockaddr_un address;
    const int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0 || !socket_address(path, address)
        || connect(fd, reinterpret_cast<sockaddr*>(&address), sizeof(address)) != 0)
    {
        const std::string problem = std::strerror(errno);
        if (fd >= 0)
            close(fd);
        return finish(false, nullptr, "connect-failed", problem, kExitConnectFailed);
    }
    set_timeout(fd, timeout);
    write_all(fd, json{{"operation", "counter-increment"}, {"index", index}, {"by", by}}.dump() + "\n");
    std::string line;
    const bool answered = read_line(fd, line);
    close(fd);
    const auto reply = answered ? json::parse(line, nullptr, false) : json();
    if (!reply.is_object() || !reply.value("ok", false))
        return finish(true, nullptr, "failed", reply.is_object() ? reply.value("message", "") : "no answer", kExitFailed);
    return finish(true, reply["value"], nullptr, "", kExitOK);
}

} // namespace adapter
