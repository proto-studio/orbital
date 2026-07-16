#!/usr/bin/env bash
# Install host deps needed to *drive* a V8 build on GitHub's Linux runners.
# The actual C++ compiler is Chromium's bundled clang (third_party/llvm-build),
# downloaded by gclient sync — Ubuntu clang-19 is too old for V8 15.x.
set -euo pipefail

# Prefer Azure's ARM ports mirror — GHA ARM runners are in Azure and often
# cannot reach ports.ubuntu.com (IPv6 unreachable or IPv4 timeouts).
sudo find /etc/apt -type f \( -name '*.list' -o -name '*.sources' \) -print0 \
  | xargs -0 -r sudo sed -i \
      -e 's|http://ports\.ubuntu\.com/ubuntu-ports|http://azure.ports.ubuntu.com/ubuntu-ports|g' \
      -e 's|https://ports\.ubuntu\.com/ubuntu-ports|http://azure.ports.ubuntu.com/ubuntu-ports|g'

# Force IPv4 + retries: ARM runners frequently lack working IPv6 to Ubuntu.
sudo tee /etc/apt/apt.conf.d/99ci-hardening >/dev/null <<'EOF'
Acquire::ForceIPv4 "true";
Acquire::Retries "5";
Acquire::http::Timeout "30";
Acquire::https::Timeout "30";
EOF

# No clang-* package: V8 builds with //third_party/llvm-build.
PKGS=(
  build-essential
  cmake
  curl
  file
  git
  lsb-release
  ninja-build
  pkg-config
  python3
  python3-pip
  xz-utils
  zstd
  libglib2.0-dev
)

# Full retry of update+install. Do NOT use --fix-missing: when mirrors drop
# mid-fetch it leaves apt in "ordering was unable to handle the media swap".
ok=0
for attempt in 1 2 3 4 5; do
  echo ">>> apt install attempt ${attempt}/5"
  sudo apt-get clean
  sudo rm -rf /var/lib/apt/lists/*
  if sudo apt-get update \
    && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y "${PKGS[@]}"; then
    ok=1
    break
  fi
  echo ">>> apt attempt ${attempt} failed; waiting before retry..."
  sleep $((attempt * 10))
done

if [ "$ok" -ne 1 ]; then
  echo "ERROR: failed to install V8 host dependencies after 5 attempts." >&2
  exit 1
fi

echo ">>> Linux V8 host deps ready (clang comes from Chromium's llvm-build)."
