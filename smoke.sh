#!/usr/bin/env bash
# 冒烟测试：安全边界 + 主要接口。跑完自动停掉服务。
set -u
cd "$(dirname "$0")"

taskkill //F //IM dsh-cleaner.exe > /dev/null 2>&1
./dsh-cleaner.exe --no-browser > _run.log 2>&1 &
sleep 3

URL=$(grep -o 'http://[0-9.:]*/?t=[a-f0-9]*' _run.log | head -1)
TOKEN=$(echo "$URL" | grep -o 't=[a-f0-9]*' | cut -d= -f2)
PORT=$(echo "$URL" | sed -E 's#.*:([0-9]+)/.*#\1#')
BASE="http://127.0.0.1:$PORT"

code() { curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "$@"; }

check() { # 名称 期望 实际
  if [ "$2" = "$3" ]; then printf "  [OK]   %-36s %s\n" "$1" "$3"
  else printf "  [FAIL] %-36s 期望 %s 实际 %s\n" "$1" "$2" "$3"; fi
}

post() { # 路径 body -> 状态码
  curl -s --noproxy '*' -o /dev/null -w '%{http_code}' \
    -X POST -H "X-Token: $TOKEN" -H 'Content-Type: application/json' -d "$2" "$BASE$1"
}
body() { # 路径 body -> 响应体
  curl -s --noproxy '*' -X POST -H "X-Token: $TOKEN" -H 'Content-Type: application/json' -d "$2" "$BASE$1"
}

echo "端口 $PORT，token ${TOKEN:0:8}…"
echo
echo "== 安全边界 =="
check "无 token 访问首页" 403 "$(code "$BASE/")"
check "带 token 访问首页" 200 "$(code "$BASE/?t=$TOKEN")"
check "token 错误" 403 "$(code "$BASE/?t=wrong")"
check "无 token 调 /api/state" 403 "$(code "$BASE/api/state")"
check "伪造 Host 调接口" 403 "$(code -H "X-Token: $TOKEN" -H "Host: evil.com" "$BASE/api/state")"
check "伪造 Host 调首页" 403 "$(code "$BASE/?t=$TOKEN" -H "Host: evil.com")"

echo
echo "== 接口可用性 =="
check "app.js" 200 "$(code "$BASE/app.js")"
check "style.css" 200 "$(code "$BASE/style.css")"
check "favicon.png" 200 "$(code "$BASE/favicon.png")"
check "GET /api/state" 200 "$(code -H "X-Token: $TOKEN" "$BASE/api/state")"
check "GET /api/candidates" 200 "$(code -H "X-Token: $TOKEN" "$BASE/api/candidates")"
check "POST /api/ping" 200 "$(post /api/ping '{}')"

echo
echo "== 危险接口的路径校验（看响应体确认是校验拦下的）=="
for d in 'C:\\Windows' 'E:\\DSH-Home' 'E:\\DSH-Home\\trash\\..\\storages' '/etc/passwd'; do
  s=$(post /api/purge "{\"dir\":\"$d\"}")
  m=$(body /api/purge "{\"dir\":\"$d\"}" | head -c 100)
  printf "  purge %-32s → %s  %s\n" "$d" "$s" "$m"
done
for d in 'C:\\Windows' 'E:\\DSH-Home\\trash'; do
  s=$(post /api/restore "{\"dir\":\"$d\"}")
  m=$(body /api/restore "{\"dir\":\"$d\"}" | head -c 100)
  printf "  restore %-30s → %s  %s\n" "$d" "$s" "$m"
done

echo
echo "== 数据接口抽查 =="
curl -s --noproxy '*' -H "X-Token: $TOKEN" "$BASE/api/state" > _state.json
node -e '
const d = JSON.parse(require("fs").readFileSync("_state.json", "utf8"));
const bad = [];
if (!Array.isArray(d.sessions)) bad.push("sessions 不是数组");
if (!Array.isArray(d.trash)) bad.push("trash 不是数组");
if (!Array.isArray(d.workspaces)) bad.push("workspaces 不是数组");
if (d.trash.some((t) => !Array.isArray(t.items))) bad.push("trash[].items 不是数组");
if (typeof d.archived !== "number") bad.push("archived 不是数字");
console.log("  会话 " + d.sessions.length + " | 工作区 " + d.workspaces.length + " | 隔离区批次 " + d.trash.length + " | 目录 " + d.home);
console.log(bad.length ? "  [FAIL] " + bad.join("; ") : "  [OK]   所有数组字段类型正确");
'

taskkill //F //IM dsh-cleaner.exe > /dev/null 2>&1
rm -f _run.log _state.json
echo
echo "服务已停止"
