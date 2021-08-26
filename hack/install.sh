#!/bin/bash

# Copyright OpenFaaS Author(s) 2020

#########################
# Repo specific content #
#########################

export OWNER="openfaas"
export REPO="faasd"

version=""

echo "Finding latest version from GitHub"
version=$(curl -sI https://github.com/$OWNER/$REPO/releases/latest | grep -i "location:" | awk -F"/" '{ printf "%s", $NF }' | tr -d '\r')
echo "$version"

if [ ! $version ]; then
  echo "Failed while attempting to get latest version"
  exit 1
fi

SUDO=sudo
if [ "$(id -u)" -eq 0 ]; then
  SUDO=
fi

verify_system() {
  if ! [ -d /run/systemd ]; then
    fatal 'Can not find systemd to use as a process supervisor for faasd'
  fi
}

has_yum() {
  [ -n "$(command -v yum)" ]
}

has_apt_get() {
  [ -n "$(command -v apt-get)" ]
}

has_pacman() {
  [ -n "$(command -v pacman)" ]
}

install_required_packages() {
  if $(has_apt_get); then
    $SUDO apt-get update -y
    $SUDO apt-get install -y curl runc bridge-utils
  elif $(has_yum); then
    $SUDO yum check-update -y
    $SUDO yum install -y curl runc
  elif $(has_pacman); then
    $SUDO pacman -Syy
    $SUDO pacman -Sy curl runc bridge-utils
  else
    fatal "Could not find apt-get, yum, or pacman. Cannot install dependencies on this OS."
    exit 1
  fi
}

install_cni_plugins() {
  cni_version=v0.8.5
  suffix=""
  arch=$(uname -m)
  case $arch in
  x86_64 | amd64)
    suffix=amd64
    ;;
  aarch64)
    suffix=arm64
    ;;
  arm*)
    suffix=arm
    ;;
  *)
    fatal "Unsupported architecture $arch"
    ;;
  esac

  $SUDO mkdir -p /opt/cni/bin
  curl -sSL https://github.com/containernetworking/plugins/releases/download/${cni_version}/cni-plugins-linux-${suffix}-${cni_version}.tgz | $SUDO tar -xvz -C /opt/cni/bin
}

install_containerd() {
  arch=$(uname -m)
  case $arch in
  x86_64 | amd64)
    curl -sLSf https://github.com/containerd/containerd/releases/download/v1.5.4/containerd-1.5.4-linux-amd64.tar.gz | $SUDO tar -xvz --strip-components=1 -C /usr/local/bin/
    ;;
  armv7l)
    curl -sSL https://github.com/alexellis/containerd-arm/releases/download/v1.5.4/containerd-1.5.4-linux-armhf.tar.gz | $SUDO tar -xvz --strip-components=1 -C /usr/local/bin/
    ;;
  aarch64)
    curl -sSL https://github.com/alexellis/containerd-arm/releases/download/v1.5.4/containerd-1.5.4-linux-arm64.tar.gz | $SUDO tar -xvz --strip-components=1 -C /usr/local/bin/
    ;;
  *)
    fatal "Unsupported architecture $arch"
    ;;
  esac

  $SUDO systemctl unmask containerd || :
  $SUDO curl -SLfs https://raw.githubusercontent.com/containerd/containerd/v1.5.4/containerd.service --output /etc/systemd/system/containerd.service
  $SUDO systemctl enable containerd
  $SUDO systemctl start containerd

  sleep 5
}

install_faasd() {
  arch=$(uname -m)
  case $arch in
  x86_64 | amd64)
    suffix=""
    ;;
  aarch64)
    suffix=-arm64
    ;;
  armv7l)
    suffix=-armhf
    ;;
  *)
    echo "Unsupported architecture $arch"
    exit 1
    ;;
  esac

  $SUDO curl -fSLs "https://github.com/openfaas/faasd/releases/download/${version}/faasd${suffix}" --output "/usr/local/bin/faasd"
  $SUDO chmod a+x "/usr/local/bin/faasd"

  mkdir -p /tmp/faasd-${version}-installation/hack
  cd /tmp/faasd-${version}-installation
  $SUDO curl -fSLs "https://raw.githubusercontent.com/openfaas/faasd/${version}/docker-compose.yaml" --output "docker-compose.yaml"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/openfaas/faasd/${version}/prometheus.yml" --output "prometheus.yml"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/openfaas/faasd/${version}/resolv.conf" --output "resolv.conf"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/openfaas/faasd/${version}/hack/faasd-provider.service" --output "hack/faasd-provider.service"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/openfaas/faasd/${version}/hack/faasd.service" --output "hack/faasd.service"
  $SUDO /usr/local/bin/faasd install
}

install_caddy() {
  if [ ! -z "${FAASD_DOMAIN}" ]; then
    arch=$(uname -m)
    case $arch in
    x86_64 | amd64)
      suffix="amd64"
      ;;
    aarch64)
      suffix=-arm64
      ;;
    armv7l)
      suffix=-armv7
      ;;
    *)
      echo "Unsupported architecture $arch"
      exit 1
      ;;
    esac

    curl -sSL "https://github.com/caddyserver/caddy/releases/download/v2.4.3/caddy_2.4.3_linux_${suffix}.tar.gz" | $SUDO tar -xvz -C /usr/bin/ caddy
    $SUDO curl -fSLs https://raw.githubusercontent.com/caddyserver/dist/master/init/caddy.service --output /etc/systemd/system/caddy.service

    $SUDO mkdir -p /etc/caddy
    $SUDO mkdir -p /var/lib/caddy
    
    if $(id caddy >/dev/null 2>&1); then
      echo "User caddy already exists."
    else
      $SUDO useradd --system --home /var/lib/caddy --shell /bin/false caddy
    fi

    $SUDO tee /etc/caddy/Caddyfile >/dev/null <<EOF
{
  email "${LETSENCRYPT_EMAIL}"
}

${FAASD_DOMAIN} {
  reverse_proxy 127.0.0.1:8080
}
EOF

    $SUDO chown --recursive caddy:caddy /var/lib/caddy
    $SUDO chown --recursive caddy:caddy /etc/caddy

    $SUDO systemctl enable caddy
    $SUDO systemctl start caddy
  else
    echo "Skipping caddy installation as FAASD_DOMAIN."
  fi
}

install_faas_cli() {
  curl -sLS https://cli.openfaas.com | $SUDO sh
}

verify_system
install_required_packages

$SUDO /sbin/sysctl -w net.ipv4.conf.all.forwarding=1
echo "net.ipv4.conf.all.forwarding=1" | $SUDO tee -a /etc/sysctl.conf

install_cni_plugins
install_containerd
install_faas_cli
install_faasd
install_caddy
