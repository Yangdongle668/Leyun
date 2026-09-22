#!/usr/bin/env bash
#
# 乐云企业网盘 · 一键部署
#
#   ./deploy.sh                 交互式部署（首次推荐）
#   ./deploy.sh --yes           全部用默认值，不提问
#   ./deploy.sh --no-office     不部署 ONLYOFFICE（不需要在线编辑时可省下约 2GB 内存）
#   ./deploy.sh --no-https      不占用 80/443（前面已有 Nginx 等反向代理时用）
#   ./deploy.sh --port 9000     指定网盘端口
#
# 脚本只做三件事：检查环境、生成配置、拉起容器。反复执行是安全的。

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

readonly ENV_FILE="$SCRIPT_DIR/.env"
readonly CONFIG_FILE="$SCRIPT_DIR/config.yaml"

# ---------- 输出 ----------
if [ -t 1 ]; then
  C_RESET='\033[0m'; C_BOLD='\033[1m'; C_DIM='\033[2m'
  C_RED='\033[31m'; C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_BLUE='\033[36m'
else
  C_RESET=''; C_BOLD=''; C_DIM=''; C_RED=''; C_GREEN=''; C_YELLOW=''; C_BLUE=''
fi

info()  { printf "${C_BLUE}▸${C_RESET} %s\n" "$*"; }
ok()    { printf "${C_GREEN}✓${C_RESET} %s\n" "$*"; }
warn()  { printf "${C_YELLOW}!${C_RESET} %s\n" "$*"; }
die()   { printf "${C_RED}✗ %s${C_RESET}\n" "$*" >&2; exit 1; }
title() {
  printf "\n${C_BOLD}%s${C_RESET}\n" "$*"
  printf "${C_DIM}%s${C_RESET}\n" "────────────────────────────────────────────────────────"
}

# ---------- 参数 ----------
ASSUME_YES=0
WITH_OFFICE=1
LEYUN_PORT=8080
ONLYOFFICE_PORT=8081
PUBLIC_HOST=""
# 80/443 用于「域名与 HTTPS」的自动证书。--no-https 可以主动不占这两个端口。
WITH_TLS_PORTS=1
# 不占 80/443 时映射到这两个高位端口：容器里那两个监听不会有人访问，
# 但 compose 的 ports 写法没法"有条件地不映射"，只能挪到无害的地方。
TLS_FALLBACK_HTTP=18080
TLS_FALLBACK_HTTPS=18443

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)        ASSUME_YES=1; shift ;;
    --no-office)     WITH_OFFICE=0; shift ;;
    --no-https)      WITH_TLS_PORTS=0; shift ;;
    --port)          LEYUN_PORT="${2:?--port 需要一个端口号}"; shift 2 ;;
    --office-port)   ONLYOFFICE_PORT="${2:?--office-port 需要一个端口号}"; shift 2 ;;
    --host)          PUBLIC_HOST="${2:?--host 需要一个地址}"; shift 2 ;;
    -h|--help)       usage ;;
    *)               die "未知参数：$1（用 --help 查看用法）" ;;
  esac
done

# ---------- 环境检查 ----------
title "1/5 检查运行环境"

command -v docker >/dev/null 2>&1 || die "未找到 docker，请先安装：https://docs.docker.com/engine/install/"

if docker compose version >/dev/null 2>&1; then
  DC=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  DC=(docker-compose)
else
  die "未找到 docker compose，请安装 Docker Compose 插件"
fi

docker info >/dev/null 2>&1 || die "Docker 守护进程未运行，或当前用户无权访问（试试 sudo，或把用户加入 docker 组）"
ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '已就绪')"

# 端口占用检查：被占了现在提示，总比构建十分钟后失败强。
port_busy() {
  local p="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$p\$"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$p\$"
  else
    return 1
  fi
}

# 端口是不是被乐云自己占着。是的话不算冲突，重新部署会替换掉。
held_by_leyun() {
  [ -f "$ENV_FILE" ] && docker ps --format '{{.Names}}' | grep -qE '^(leyun|leyun-onlyoffice)$'
}

# 80/443 是不是乐云自己占着。
#
# 不能用 held_by_leyun 代替：那个只说明"乐云在跑"。上一轮因为 Nginx 占着 80
# 而降级到高位端口时，乐云同样在跑，但 80 是 Nginx 的——若据此判成"自己占的"，
# 这一轮就会把 80 写回 .env，容器起不来。所以直接看容器到底映射了哪些端口。
leyun_holds_tls_ports() {
  docker ps --filter 'name=^leyun$' --format '{{.Ports}}' 2>/dev/null |
    grep -qE '(^|[^0-9])(80|443)->'
}

check_port() {
  local p="$1" name="$2"
  if port_busy "$p"; then
    if held_by_leyun; then
      warn "端口 $p 当前被乐云自己占用，将在重启时释放"
    else
      die "端口 $p 已被占用（$name），请用 --port / --office-port 换一个端口"
    fi
  fi
}
check_port "$LEYUN_PORT" "网盘"
[ "$WITH_OFFICE" -eq 1 ] && check_port "$ONLYOFFICE_PORT" "ONLYOFFICE"

# 80/443 与上面几个不同：被占用了也不该让部署失败。
# 它们只影响「域名与 HTTPS」里的自动证书，网盘本身照常跑。
# 占着 80 的多半是 Nginx，那种部署本来就该由 Nginx 管证书。
TLS_AVAILABLE=1
if [ "$WITH_TLS_PORTS" -eq 0 ]; then
  TLS_AVAILABLE=0
  LEYUN_HTTP_PORT="$TLS_FALLBACK_HTTP"
  LEYUN_HTTPS_PORT="$TLS_FALLBACK_HTTPS"
elif { port_busy 80 || port_busy 443; } && ! leyun_holds_tls_ports; then
  TLS_AVAILABLE=0
  LEYUN_HTTP_PORT="$TLS_FALLBACK_HTTP"
  LEYUN_HTTPS_PORT="$TLS_FALLBACK_HTTPS"
  warn "80 或 443 已被占用，自动 HTTPS 不可用（网盘本身不受影响）"
  warn "  若前面已有 Nginx 等反向代理，证书交给它管即可，见 docs/domain-https.md"
fi

# 磁盘空间：镜像加上 ONLYOFFICE 大约要 4GB。
avail_kb=$(df -Pk "$SCRIPT_DIR" | awk 'NR==2 {print $4}')
need_kb=$([ "$WITH_OFFICE" -eq 1 ] && echo 4194304 || echo 1048576)
if [ "${avail_kb:-0}" -lt "$need_kb" ]; then
  warn "当前目录可用空间约 $((avail_kb / 1024)) MB，建议至少 $((need_kb / 1024)) MB"
fi

# ---------- 交互 ----------
title "2/5 部署参数"

ask() { # 变量名 提示 默认值
  local __var="$1" __prompt="$2" __default="$3" __input=""
  if [ "$ASSUME_YES" -eq 1 ] || [ ! -t 0 ]; then
    printf -v "$__var" '%s' "$__default"
    printf "  %-22s %s ${C_DIM}(默认)${C_RESET}\n" "$__prompt" "$__default"
    return
  fi
  read -r -p "  $__prompt [$__default]: " __input || true
  printf -v "$__var" '%s' "${__input:-$__default}"
}

# 猜一个对外地址，方便用户直接复制访问链接。
guess_host() {
  local ip
  ip=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}') || true
  [ -z "$ip" ] && ip=$(hostname -I 2>/dev/null | awk '{print $1}') || true
  echo "${ip:-localhost}"
}
[ -z "$PUBLIC_HOST" ] && PUBLIC_HOST="$(guess_host)"

ask PUBLIC_HOST      "访问本机的地址/域名" "$PUBLIC_HOST"
ask LEYUN_PORT       "网盘端口"           "$LEYUN_PORT"
ask ADMIN_USERNAME   "超级管理员用户名"    "admin"
ask ADMIN_PASSWORD   "超级管理员初始口令"  "admin"
if [ "$WITH_OFFICE" -eq 1 ]; then
  ask ONLYOFFICE_PORT "ONLYOFFICE 端口"   "$ONLYOFFICE_PORT"
fi

# ---------- 生成配置 ----------
title "3/5 生成配置"

gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

if [ -f "$ENV_FILE" ]; then
  # 已有密钥必须原样保留：换掉 JWT 密钥会让所有人当场掉线，
  # 换掉 Office 密钥会让在线编辑直接握手失败。
  # shellcheck disable=SC1090
  set -a; . "$ENV_FILE"; set +a
  ok "沿用已有的 .env（密钥保持不变）"
fi
JWT_SECRET="${LEYUN_JWT_SECRET:-$(gen_secret)}"
OFFICE_SECRET="${LEYUN_OFFICE_JWT_SECRET:-$(gen_secret)}"

OFFICE_ENABLED=$([ "$WITH_OFFICE" -eq 1 ] && echo true || echo false)

cat > "$ENV_FILE" <<EOF
# 由 deploy.sh 生成，可手工修改后执行 ./update.sh 生效。
# 注意：两个 SECRET 一旦改动，所有人会掉线、在线编辑会失效。
LEYUN_VERSION=1.0.0
TZ=Asia/Shanghai

LEYUN_PORT=${LEYUN_PORT}
# 自动 HTTPS 用的端口。被占用时 deploy.sh 会改成高位端口，
# 那种情况下证书应当交给前面的反向代理管。
LEYUN_HTTP_PORT=${LEYUN_HTTP_PORT:-80}
LEYUN_HTTPS_PORT=${LEYUN_HTTPS_PORT:-443}
LEYUN_JWT_SECRET=${JWT_SECRET}
LEYUN_ADMIN_USERNAME=${ADMIN_USERNAME}
LEYUN_ADMIN_PASSWORD=${ADMIN_PASSWORD}

LEYUN_OFFICE_ENABLED=${OFFICE_ENABLED}
LEYUN_OFFICE_PUBLIC_URL=http://${PUBLIC_HOST}:${ONLYOFFICE_PORT}
LEYUN_OFFICE_JWT_SECRET=${OFFICE_SECRET}
# PDF 在线编辑需要 Document Server 8.1+；对接老版本时改成 false
LEYUN_OFFICE_PDF_EDIT=true
ONLYOFFICE_PORT=${ONLYOFFICE_PORT}
ONLYOFFICE_VERSION=8.2
EOF
chmod 600 "$ENV_FILE"
ok "已写入 .env"

if [ ! -f "$CONFIG_FILE" ]; then
  cp config.example.yaml "$CONFIG_FILE"
  ok "已从 config.example.yaml 生成 config.yaml"
else
  ok "沿用已有的 config.yaml"
fi

mkdir -p data
ok "数据目录 ./data 就绪"

# ---------- 构建并启动 ----------
title "4/5 构建镜像并启动（首次需要几分钟）"

if [ "$WITH_OFFICE" -eq 0 ]; then
  warn "未部署 ONLYOFFICE，Office 在线编辑不可用（之后可重跑 ./deploy.sh 补上）"
  # --no-deps 让 compose 不去理会 depends_on，只起网盘本体。
  "${DC[@]}" up -d --build --no-deps leyun
else
  "${DC[@]}" up -d --build
fi

# ---------- 等待就绪 ----------
title "5/5 等待服务就绪"

health_url="http://127.0.0.1:${LEYUN_PORT}/api/v1/health"
ready=0
for i in $(seq 1 60); do
  if curl -fsS --noproxy '*' --max-time 3 "$health_url" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
  printf "."
done
printf "\n"

if [ "$ready" -ne 1 ]; then
  warn "服务未在两分钟内就绪，查看日志排查："
  echo "    ${DC[*]} logs -f leyun"
  exit 1
fi
ok "乐云已启动"

if [ "$WITH_OFFICE" -eq 1 ]; then
  info "ONLYOFFICE 首次启动需要 1-3 分钟自检，稍后再试在线编辑即可"
fi

# ---------- 收尾 ----------
printf "\n${C_GREEN}${C_BOLD}部署完成${C_RESET}\n"
printf "${C_DIM}%s${C_RESET}\n" "────────────────────────────────────────────────────────"
printf "  访问地址    ${C_BOLD}http://%s:%s${C_RESET}\n" "$PUBLIC_HOST" "$LEYUN_PORT"
printf "  管理员      %s\n" "$ADMIN_USERNAME"
printf "  初始口令    %s\n" "$ADMIN_PASSWORD"
[ "$WITH_OFFICE" -eq 1 ] && printf "  文档服务    http://%s:%s\n" "$PUBLIC_HOST" "$ONLYOFFICE_PORT"
printf "${C_DIM}%s${C_RESET}\n" "────────────────────────────────────────────────────────"
printf "  该账号是系统中唯一能开通其他账号的角色，本系统不提供自助注册。\n"
printf "  登录后请到「管理后台 → 部门管理」建好组织架构，再开通成员账号。\n"
if [ "$TLS_AVAILABLE" -eq 1 ]; then
  printf "  有域名的话，到「管理后台 → 域名与 HTTPS」填上即可自动申请证书并续期。\n"
else
  printf "  ${C_DIM}80/443 未占用于本服务，内置的自动 HTTPS 不可用（见 docs/domain-https.md）。${C_RESET}\n"
fi
if [ "$ADMIN_PASSWORD" = "admin" ]; then
  printf "  ${C_YELLOW}⚠ 初始口令仍是 admin，请登录后立即修改。${C_RESET}\n"
fi
printf "${C_DIM}%s${C_RESET}\n\n" "────────────────────────────────────────────────────────"
printf "  常用命令\n"
printf "    升级      ./update.sh\n"
printf "    看日志    %s logs -f leyun\n" "${DC[*]}"
printf "    停止      %s down\n" "${DC[*]}"
printf "    重启      %s restart leyun\n\n" "${DC[*]}"
