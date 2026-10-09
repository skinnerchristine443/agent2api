#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/deploy/.env"
COMPOSE=(docker compose --env-file "${ENV_FILE}" -f "${ROOT_DIR}/deploy/docker-compose.yml")

if [[ ! -f "${ENV_FILE}" ]]; then
  cp "${ROOT_DIR}/deploy/.env.example" "${ENV_FILE}"
  echo "Created ${ENV_FILE}; SQLite will generate the API key on first startup."
fi

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if ! "${COMPOSE[@]}" pull; then
  echo "Published image unavailable; building the image locally."
  "${COMPOSE[@]}" up -d --build
else
  "${COMPOSE[@]}" up -d
fi

for _ in {1..60}; do
  if curl -fsS http://127.0.0.1:3010/health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS http://127.0.0.1:3010/health >/dev/null 2>&1; then
  "${COMPOSE[@]}" ps
  "${COMPOSE[@]}" logs --no-color --tail=100 agent2api
  exit 1
fi

echo "agent2api is running at http://127.0.0.1:3010"

# 首启密钥：新版本在非终端环境（容器）写入数据目录 initial-keys.txt（0600），
# 不再进入容器日志；旧镜像仍打印在日志中。优先取文件，回退日志提取。
stored_keys="$("${COMPOSE[@]}" exec -T agent2api cat /data/initial-keys.txt 2>/dev/null || true)"
if [[ -n "${stored_keys}" ]]; then
  printf '%s\n' "${stored_keys}"
  echo "（以上密钥已存入容器数据目录 /data/initial-keys.txt〔0600〕；确认留存后可自行删除）"
else
  recent_logs="$("${COMPOSE[@]}" logs --no-color --since "${started_at}" agent2api 2>/dev/null || true)"
  new_key="$(printf '%s\n' "${recent_logs}" | grep -F 'initialized API key' || true)"
  if [[ -n "${new_key}" ]]; then
    printf '%s\n' "${new_key}"
  else
    echo "The API key is already stored in the SQLite database."
  fi

  # 运维（控制台）密钥同为独立随机值；一并提取（仅旧镜像需要）。
  new_console_key="$(printf '%s\n' "${recent_logs}" | grep -F 'initialized console (operator) key' || true)"
  if [[ -n "${new_console_key}" ]]; then
    printf '%s\n' "${new_console_key}"
  fi
fi
