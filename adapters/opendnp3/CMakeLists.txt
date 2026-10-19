# SPDX-License-Identifier: MIT
#
# The opendnp3 adapter. Built by the Dockerfile against an opendnp3 installed
# in OPENDNP3_PREFIX.

cmake_minimum_required(VERSION 3.16)
project(dnp3-interop-opendnp3 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

set(OPENDNP3_PREFIX "/opt/opendnp3" CACHE PATH "Where opendnp3 is installed")
set(ADAPTER_VERSION "dev" CACHE STRING "Release of dnp3-interop")
set(UPSTREAM_VERSION "unknown" CACHE STRING "opendnp3 version")
set(UPSTREAM_REVISION "unknown" CACHE STRING "opendnp3 commit")

find_package(nlohmann_json 3 REQUIRED)
find_package(Threads REQUIRED)

add_executable(dnp3-interop
  src/main.cpp
  src/util.cpp
  src/fixture.cpp
  src/stack.cpp
  src/server.cpp
  src/client.cpp)

# The library's headers are not held to the adapter's warning level.
target_include_directories(dnp3-interop SYSTEM PRIVATE "${OPENDNP3_PREFIX}/include")
target_compile_options(dnp3-interop PRIVATE -Wall -Wextra -Werror)
target_compile_definitions(dnp3-interop PRIVATE
  ADAPTER_VERSION="${ADAPTER_VERSION}"
  UPSTREAM_VERSION="${UPSTREAM_VERSION}"
  UPSTREAM_REVISION="${UPSTREAM_REVISION}")
target_link_libraries(dnp3-interop PRIVATE
  "${OPENDNP3_PREFIX}/lib/libopendnp3.a"
  nlohmann_json::nlohmann_json
  Threads::Threads
  -static-libstdc++ -static-libgcc)
