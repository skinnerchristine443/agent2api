#!/usr/bin/env bash
# 关键包覆盖率下限守卫：任一下限被击穿即失败（exit 1）。
#
# 下限 = 实测值向下取整并留 ≥1.5pt 余量（2026-10-07 T54 五项；2026-10-09
# 全量审查追加七项；同日测试补强三批随覆盖提升棘轮上调）；调整下限
# 属于评审决定（与前端 check-conventions 的棘轮同精神：只增不减）。
# 用法：./scripts/check-coverage.sh（需 Go 在 PATH）。
set -euo pipefail

cd "$(dirname "$0")/.."

targets=(
  "internal/console 95"
  "internal/providers 94"
  "internal/accounts 95"
  "internal/gateway 73"
  "internal/runtime 77"
  "internal/control 72"
  "internal/translate 70"
  "internal/update 63"
  "internal/updater 64"
  "internal/logs 86"
)

status=0
for entry in "${targets[@]}"; do
  pkg="${entry% *}"
  min="${entry##* }"

  if ! out="$(go test -count=1 -cover "./${pkg}/" 2>&1)"; then
    echo "FAIL: ${pkg}: go test failed"
    printf '%s\n' "${out}" | tail -5
    status=1
    continue
  fi

  line="$(printf '%s\n' "${out}" | tail -1)"
  cov="$(printf '%s' "${line}" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p')"
  if [ -z "${cov}" ]; then
    echo "FAIL: ${pkg}: cannot parse coverage from: ${line}"
    status=1
    continue
  fi

  if awk -v a="${cov}" -v b="${min}" 'BEGIN { exit !(a < b) }'; then
    echo "FAIL: ${pkg}: coverage ${cov}% < 下限 ${min}%"
    status=1
  else
    echo "ok:   ${pkg}: coverage ${cov}% (下限 ${min}%)"
  fi
done

if [ "${status}" -ne 0 ]; then
  echo "覆盖率下限守卫失败"
fi
exit "${status}"
