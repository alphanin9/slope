#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-slope-sandbox}"
SERVICE_USER="${SERVICE_USER:-slope}"
SERVICE_GROUP="${SERVICE_GROUP:-${SERVICE_USER}}"
INSTALL_BIN="${INSTALL_BIN:-/usr/local/bin/slope-sandbox}"
CONFIG_DIR="${CONFIG_DIR:-/etc/slope}"
CONFIG_PATH="${CONFIG_PATH:-${CONFIG_DIR}/config.yaml}"
STATE_DIR="${STATE_DIR:-/var/lib/slope}"
CONFIG_SRC="${CONFIG_SRC:-config.example.yaml}"
OVERWRITE_CONFIG="${OVERWRITE_CONFIG:-0}"
ENABLE_SERVICE="${ENABLE_SERVICE:-1}"
START_SERVICE="${START_SERVICE:-1}"

ROOT_CMD=()
if [[ "${EUID}" -ne 0 ]]; then
  if ! command -v sudo >/dev/null 2>&1; then
    echo "error: run as root or install sudo" >&2
    exit 1
  fi
  ROOT_CMD=(sudo)
fi

run_root() {
  "${ROOT_CMD[@]}" "$@"
}

write_root_file() {
  local path="$1"
  run_root install -d -m 0755 "$(dirname "$path")"
  if [[ ${#ROOT_CMD[@]} -eq 0 ]]; then
    cat >"$path"
  else
    sudo tee "$path" >/dev/null
  fi
}

if [[ ! -f go.mod ]]; then
  echo "error: run this script from the repository root" >&2
  exit 1
fi

if [[ ! -f "${CONFIG_SRC}" ]]; then
  echo "error: config source not found: ${CONFIG_SRC}" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is required" >&2
  exit 1
fi

if ! command -v systemctl >/dev/null 2>&1; then
  echo "error: systemctl is required" >&2
  exit 1
fi

echo "building ${SERVICE_NAME}"
mkdir -p build
go build -tags libvirt -trimpath -ldflags="-s -w" -o build/slope-sandbox ./cmd/slope-sandbox

echo "installing binary to ${INSTALL_BIN}"
run_root install -D -m 0755 build/slope-sandbox "${INSTALL_BIN}"

echo "creating state and config directories"
run_root install -d -m 0755 "${CONFIG_DIR}"
run_root install -d -m 0750 "${STATE_DIR}"

if ! getent group "${SERVICE_GROUP}" >/dev/null 2>&1; then
  echo "creating system group ${SERVICE_GROUP}"
  run_root groupadd --system "${SERVICE_GROUP}"
fi

NOLOGIN_SHELL="$(command -v nologin || true)"
if [[ -z "${NOLOGIN_SHELL}" ]]; then
  NOLOGIN_SHELL="/usr/sbin/nologin"
fi

if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
  echo "creating system user ${SERVICE_USER}"
  run_root useradd --system --home-dir "${STATE_DIR}" --shell "${NOLOGIN_SHELL}" --gid "${SERVICE_GROUP}" "${SERVICE_USER}"
fi

supplementary_groups=()
for group in libvirt kvm; do
  if getent group "${group}" >/dev/null 2>&1; then
    supplementary_groups+=("${group}")
    run_root usermod -aG "${group}" "${SERVICE_USER}"
  fi
done

run_root chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${STATE_DIR}"

if [[ ! -f "${CONFIG_PATH}" || "${OVERWRITE_CONFIG}" == "1" ]]; then
  echo "installing config to ${CONFIG_PATH}"
  tmp_config="$(mktemp)"
  sed \
    -e "s#storage_dir: \"storage\"#storage_dir: \"${STATE_DIR}\"#" \
    -e "s#database_path: \"storage/slope.db\"#database_path: \"${STATE_DIR}/slope.db\"#" \
    "${CONFIG_SRC}" >"${tmp_config}"
  run_root install -m 0640 -o root -g "${SERVICE_GROUP}" "${tmp_config}" "${CONFIG_PATH}"
  rm -f "${tmp_config}"
else
  echo "keeping existing config at ${CONFIG_PATH}"
fi

echo "installing systemd unit"
supplementary_line=""
if [[ "${#supplementary_groups[@]}" -gt 0 ]]; then
  supplementary_line="SupplementaryGroups=${supplementary_groups[*]}"
fi

write_root_file "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Slope CTF sandbox service
After=network-online.target libvirtd.service virtqemud.service
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
${supplementary_line}
ExecStart=${INSTALL_BIN} -config ${CONFIG_PATH}
WorkingDirectory=${STATE_DIR}
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=${STATE_DIR}

[Install]
WantedBy=multi-user.target
EOF

echo "reloading systemd"
run_root systemctl daemon-reload

if [[ "${ENABLE_SERVICE}" == "1" ]]; then
  echo "enabling ${SERVICE_NAME}.service"
  run_root systemctl enable "${SERVICE_NAME}.service"
fi

if [[ "${START_SERVICE}" == "1" ]]; then
  echo "starting ${SERVICE_NAME}.service"
  run_root systemctl restart "${SERVICE_NAME}.service"
  run_root systemctl --no-pager --full status "${SERVICE_NAME}.service" || true
fi

cat <<EOF

installed ${SERVICE_NAME}

Config: ${CONFIG_PATH}
State:  ${STATE_DIR}
Logs:   journalctl -u ${SERVICE_NAME}.service -f

Edit ${CONFIG_PATH} for your VM names/snapshots, then run:
  sudo systemctl restart ${SERVICE_NAME}.service
EOF
