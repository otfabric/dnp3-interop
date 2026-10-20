#!/usr/bin/env bash
# Makes a throw-away PKI for the TLS self-tests: a CA, an outstation
# certificate and a master certificate, ECDSA P-256, PEM, keys in PKCS #8.
#
# Usage: test-pki.sh <directory> [<server name>]
#
# Writes ca.pem, server.pem, server.key, client.pem, client.key, and a second
# CA (other-ca.pem) with a server certificate of its own (other-server.pem,
# other-server.key) that the first CA does not vouch for. The server
# certificate carries the subject alternative names <server name> (default
# dnp3-interop-server), localhost and 127.0.0.1. Valid for 30 days: these
# keys exist for one test run and are never committed.
#
# Needs openssl (OpenSSL 1.1.1 or later, or LibreSSL 3).

set -euo pipefail
DIR="${1:?usage: test-pki.sh <directory> [<server name>]}"
NAME="${2:-dnp3-interop-server}"
mkdir -p "${DIR}"
cd "${DIR}"

# Named curve: LibreSSL otherwise writes the explicit curve parameters, which
# rustls and Go refuse (the stepfunc and dscsystems adapters cannot load them).
key() { openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -pkeyopt ec_param_enc:named_curve -out "$1" 2>/dev/null; }

ca() {  # ca <name> <subject>
    key "$1.key"
    cat > "$1.ext" <<EOF
basicConstraints=critical,CA:TRUE
keyUsage=critical,keyCertSign,cRLSign
subjectKeyIdentifier=hash
EOF
    openssl req -new -key "$1.key" -subj "/CN=$2" -out "$1.csr" 2>/dev/null
    openssl x509 -req -in "$1.csr" -signkey "$1.key" -days 30 -sha256 -extfile "$1.ext" -out "$1.pem" 2>/dev/null
}

leaf() {  # leaf <name> <ca> <subject> <extendedKeyUsage> [<subjectAltName>]
    key "$1.key"
    {
        echo "basicConstraints=critical,CA:FALSE"
        echo "keyUsage=critical,digitalSignature"
        echo "extendedKeyUsage=$4"
        echo "authorityKeyIdentifier=keyid"
        [[ -n "${5:-}" ]] && echo "subjectAltName=$5"
    } > "$1.ext"
    openssl req -new -key "$1.key" -subj "/CN=$3" -out "$1.csr" 2>/dev/null
    openssl x509 -req -in "$1.csr" -CA "$2.pem" -CAkey "$2.key" -CAcreateserial -days 30 -sha256 \
        -extfile "$1.ext" -out "$1.pem" 2>/dev/null
}

ca ca "dnp3-interop test CA"
leaf server ca "${NAME}" serverAuth "DNS:${NAME},DNS:localhost,IP:127.0.0.1"
leaf client ca "dnp3-interop master" clientAuth
ca other-ca "dnp3-interop other CA"
leaf other-server other-ca "${NAME}" serverAuth "DNS:${NAME},DNS:localhost,IP:127.0.0.1"

rm -f ./*.csr ./*.ext ./*.srl ca.key other-ca.key
# Readable by the images' user (uid 10001): test keys, nothing to protect.
chmod 644 ./*.pem ./*.key
