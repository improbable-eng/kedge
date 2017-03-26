#!/bin/bash
# Regenerate the self-signed certificate for local host.

#set -e
set -x

#openssl genrsa -out ca.key 4096   # note, foo is the password to ca.key
#openssl req -nodes -new -x509 -days 1024 -key ca.key -out ca.crt \
# -subj "/C=UK/O=CA/CN=example.com Self-Signed CA"

echo "Generating Client Cert  cert (ca.key and ca.crt)"
openssl genrsa -out client.key  4096
openssl req -nodes  -new -key client.key -out client.csr  \
 -subj "/C=UK/O=admins/O=eng/CN=someone@example.com/EA=someone@example.com"
openssl x509 -req -days 1024 -in client.csr -CA ca.crt -CAkey ca.key -passin pass:foo -set_serial 01 -out client.crt
openssl pkcs12 -export -nodes -out client.p12  -inkey client.key -in client.crt -certfile ca.crt

#echo "Generating a 'localhost' Server Cert"
#openssl genrsa -out client.key  4096
#openssl req -nodes  -new -key localhost.key -out localhost.csr  \
# -subj "/C=UK/O=admins/O=eng/CN=localhost"
#openssl x509 -req -days 1024 -in localhost.csr -CA ca.crt -CAkey ca.key -passin pass:foo -set_serial 10 -out localhost.crt
