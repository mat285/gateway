#!/bin/sh

set -e

VERSION=$1
if [ -z "$VERSION" ]; then
    VERSION=$(curl -sL https://api.github.com/repos/mat285/gateway/releases/latest | jq -r .name)
    echo "No version specified, using latest release: ${VERSION}"
fi

which kubectl >/dev/null 2>&1 || {
    echo "kubectl is not installed. Please install kubectl first."
    exit 1
}

OS=$(uname -s | dd conv=lcase 2>/dev/null)
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    ARCH="amd64"
elif [ "$ARCH" = "aarch64" ]; then
    ARCH="arm64"
fi

if [ "$ARCH" != "amd64" ] && [ "$ARCH" != "arm64" ]; then
    echo "Unsupported architecture: ${ARCH}"
    exit 1
fi

if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
    echo "Unsupported OS: ${OS}"
    exit 1
fi

echo "Detected OS: ${OS}, Architecture: ${ARCH}"

sudo systemctl stop gateway.service || true
sudo systemctl disable gateway.service || true
echo "Installing Gateway server version ${VERSION}..."

curl --fail-with-body -Lo gateway https://github.com/mat285/gateway/releases/download/${VERSION}/gateway-server_${OS}_${ARCH}
sudo mv gateway /bin/gateway
sudo chown root:root /bin/gateway
sudo chmod a+x /bin/gateway

curl --fail-with-body -Lo gateway.service https://github.com/mat285/gateway/releases/download/${VERSION}/gateway.service
sudo mv gateway.service /etc/systemd/system/gateway.service
sudo chmod 644 /etc/systemd/system/gateway.service
sudo chown root:root /etc/systemd/system/gateway.service

curl --fail-with-body -Lo gateway https://github.com/mat285/gateway/releases/download/${VERSION}/example.yml
sudo mkdir -p /etc/gateway
sudo mv gateway /etc/gateway/example.yml
sudo chown root:root /etc/gateway/example.yml

sudo mkdir -p /etc/gateway
sudo chown root:root /etc/gateway

sudo systemctl daemon-reload
sudo systemctl enable gateway.service
sudo systemctl start gateway.service
echo "Gateway server installed and started successfully."
