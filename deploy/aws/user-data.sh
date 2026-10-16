#!/bin/bash
# First boot for the single Forge host. Installs Docker and a small swap
# file so image builds do not OOM a 2 GB instance. It does not clone the
# repository or write secrets: those stay in the operator's .env.
set -euo pipefail
dnf update -y
dnf install -y docker git
systemctl enable --now docker
if ! swapon --show | grep -q /swapfile; then
  fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
EOF
systemctl restart docker
usermod -aG docker ec2-user || true
# Compose v2 plugin from the Docker repo is optional; the engine above is enough
# for `docker compose` on Amazon Linux 2023 when the package exists.
dnf install -y docker-compose-plugin || true
