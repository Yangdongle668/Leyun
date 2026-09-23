#!/usr/bin/env bash
#
# 乐云企业网盘 · 升级
#
#   ./update.sh                 拉取最新代码 → 备份数据 → 重建镜像 → 重启 → 健康检查
#   ./update.sh --no-pull       不拉代码，只用当前工作区重建（改过配置后常用）
#   ./update.sh --no-backup     跳过备份（不推荐）
#   ./update.sh --rollback      回滚到上一次升级前的状态
#   ./update.sh --keep 10       备份保留份数，默认 5
#
# 升级失败会自动回滚到升级前的镜像与数据，服务不会停在半截状态。

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

readonly BACKUP_DIR="$SCRIPT_DIR/backups"
readonly ENV_FILE="$SCRIPT_DIR/.env"

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

# ---------- 进度显示 ----------
#
# 升级里有几步是纯等待：打包备份、停容器、等健康检查。它们本身不出声，
# 屏幕能十几秒甚至几分钟一动不动，看着就像卡死了。下面这套东西只解决
# 这一件事——让人知道它还在跑。
#
# 构建那一步不归这里管，见 compose_up 的注释。

SPIN_FRAMES=(⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏)

# 指向一个正在变大的文件时，转圈旁边会带上它的当前大小。
SPIN_WATCH=""

spin_done() { printf "${C_GREEN}✓${C_RESET} %s  ${C_DIM}%ds${C_RESET}\n" "$1" "$2"; }

# spin_run <说明> <命令...>
#
# 后台跑命令，前台转圈并报已用时长。命令的输出收进临时文件：
# 成功了没人要看，失败了必须看得到，所以只在失败时原样吐出来。
spin_run() {
  local label="$1"; shift
  local log; log="$(mktemp)"
  local start=$SECONDS rc=0

  # 输出不是终端时（重定向到日志、CI、nohup），转圈只会刷出一屏回车，
  # 退化成一行说明。
  if [ ! -t 1 ]; then
    info "$label ..."
    "$@" >"$log" 2>&1 || rc=$?
    [ "$rc" -eq 0 ] || cat "$log" >&2
    rm -f "$log"
    return "$rc"
  fi

  "$@" >"$log" 2>&1 &
  local pid=$! i=0 extra=""
  while kill -0 "$pid" 2>/dev/null; do
    extra=""
    if [ -n "$SPIN_WATCH" ] && [ -f "$SPIN_WATCH" ]; then
      extra="  $(du -h "$SPIN_WATCH" 2>/dev/null | cut -f1)"
    fi
    # 颜色必须留在格式串里：C_DIM 这些是没展开的 \033 字面量，
    # 塞进 %s 会被原样打出来。
    printf "\r${C_BLUE}%s${C_RESET} %s  ${C_DIM}%ds%s${C_RESET}\033[K" \
      "${SPIN_FRAMES[i]}" "$label" "$((SECONDS - start))" "$extra"
    i=$(((i + 1) % ${#SPIN_FRAMES[@]}))
    sleep 0.1
  done
  wait "$pid" || rc=$?

  printf "\r\033[K"
  if [ "$rc" -eq 0 ]; then
    spin_done "$label" "$((SECONDS - start))"
  else
    warn "$label 失败"
    cat "$log" >&2
  fi
  rm -f "$log"
  return "$rc"
}

DO_PULL=1
DO_BACKUP=1
DO_ROLLBACK=0
KEEP=5

# 从第 2 行起连续的注释就是用法说明，遇到第一行非注释就停。
# 原来写的是固定行号 2,14p，文件头一改就会把 set -euo pipefail 也打出来。
usage() {
  awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    --no-pull)   DO_PULL=0; shift ;;
    --no-backup) DO_BACKUP=0; shift ;;
    --rollback)  DO_ROLLBACK=1; shift ;;
    --keep)      KEEP="${2:?--keep 需要一个数字}"; shift 2 ;;
    -h|--help)   usage ;;
    *)           die "未知参数：$1（用 --help 查看用法）" ;;
  esac
done

# ---------- 环境 ----------
[ -f "$ENV_FILE" ] || die "找不到 .env，请先执行 ./deploy.sh 完成首次部署"

if docker compose version >/dev/null 2>&1; then
  DC=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  DC=(docker-compose)
else
  die "未找到 docker compose"
fi

# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a
PORT="${LEYUN_PORT:-8080}"

# compose_up 按当初的部署形态启动。
#
# 两件事：
#
# 一是不能把 docker 的输出接到管道里。一接管道 BuildKit 就判定不是终端，
# 退回纯文本模式，再加上管道缓冲，屏幕可以几十秒一动不动——这正是
# 升级看着像卡死、而 deploy.sh 看着正常的原因（那边是直接往终端上画的）。
# 所以这里一律让 docker 自己输出，它的进度条比我们能做的任何东西都好。
#
# 二是当初用 --no-office 部署的，升级时不能顺手把 ONLYOFFICE 拉起来。
# leyun 在 compose 里 depends_on onlyoffice，点名 leyun 也会连带拉起它，
# 得靠 --no-deps 挡住——那是个几 GB 的镜像，用户明确不要的东西不该在
# 升级过程中冒出来。
compose_up() {
  if [ "${LEYUN_OFFICE_ENABLED:-true}" = "false" ]; then
    # 这里必须点名 leyun：--no-deps 只在指定了服务时才起作用，
    # 不带服务名的 up 照样会把 compose 文件里的所有服务都拉起来。
    "${DC[@]}" up -d --build --no-deps leyun
  else
    "${DC[@]}" up -d --build "$@"
  fi
}

# ---------- 备份 / 回滚 ----------
mkdir -p "$BACKUP_DIR"

make_backup() {
  local stamp; stamp="$(date +%Y%m%d-%H%M%S)"
  local target="$BACKUP_DIR/$stamp"
  mkdir -p "$target"

  # 先停服务再复制：SQLite 正在写的时候直接拷会拿到不一致的快照。
  spin_run "暂停服务以获得一致的数据快照" "${DC[@]}" stop leyun || true

  # 先把体量报出来。知道要打包多少，等起来心里有数。
  local size
  size="$(du -sh "$SCRIPT_DIR/data" 2>/dev/null | cut -f1 || true)"
  [ -n "$size" ] && info "data/ 当前 $size"

  # 转圈时带上压缩包的当前大小，能看出它确实在长。
  if command -v tar >/dev/null 2>&1; then
    SPIN_WATCH="$target/data.tar.gz"
    spin_run "打包 data/" tar -czf "$target/data.tar.gz" -C "$SCRIPT_DIR" data
    SPIN_WATCH=""
  else
    spin_run "复制 data/" cp -a "$SCRIPT_DIR/data" "$target/data"
  fi
  cp -a "$ENV_FILE" "$target/.env" 2>/dev/null || true
  cp -a "$SCRIPT_DIR/config.yaml" "$target/config.yaml" 2>/dev/null || true

  # 记下当前提交与镜像，回滚时按这个还原。
  git -C "$SCRIPT_DIR" rev-parse HEAD > "$target/commit.txt" 2>/dev/null || true
  docker image inspect "leyun:${LEYUN_VERSION:-1.0.0}" --format '{{.Id}}' \
    > "$target/image.txt" 2>/dev/null || true

  echo "$target" > "$BACKUP_DIR/latest"
  ok "备份完成：backups/$stamp"

  # 清理旧备份
  local count
  count=$(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d | wc -l)
  if [ "$count" -gt "$KEEP" ]; then
    find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d | sort | head -n "$((count - KEEP))" |
      while read -r old; do rm -rf "$old"; info "清理旧备份 $(basename "$old")"; done
  fi
}

restore_backup() {
  local target="$1"
  [ -d "$target" ] || die "备份目录不存在：$target"
  info "从 $(basename "$target") 恢复数据"
  spin_run "停止服务" "${DC[@]}" stop leyun || true
  if [ -f "$target/data.tar.gz" ]; then
    rm -rf "$SCRIPT_DIR/data"
    spin_run "解包 data/" tar -xzf "$target/data.tar.gz" -C "$SCRIPT_DIR"
  elif [ -d "$target/data" ]; then
    rm -rf "$SCRIPT_DIR/data"
    spin_run "复制 data/" cp -a "$target/data" "$SCRIPT_DIR/data"
  fi
  [ -f "$target/.env" ] && cp -a "$target/.env" "$ENV_FILE"
  [ -f "$target/config.yaml" ] && cp -a "$target/config.yaml" "$SCRIPT_DIR/config.yaml"
  ok "数据已恢复"
}

# 等服务起来。容器刚重建完，这一等可能是十几秒，必须一直有动静。
wait_healthy() {
  local url="http://127.0.0.1:${PORT}/api/v1/health"
  local start=$SECONDS i=0 limit=120
  while [ $((SECONDS - start)) -lt "$limit" ]; do
    if curl -fsS --noproxy '*' --max-time 3 "$url" >/dev/null 2>&1; then
      [ -t 1 ] && printf "\r\033[K"
      spin_done "服务已就绪" "$((SECONDS - start))"
      return 0
    fi
    # 探活两秒一次就够了，但转圈得一直转：两秒才动一下，
    # 跟不动看着没区别，照样让人以为卡住了。
    local n=0
    while [ "$n" -lt 20 ]; do
      if [ -t 1 ]; then
        printf "\r${C_BLUE}%s${C_RESET} 等待服务就绪  ${C_DIM}%ds / 最多 %ds${C_RESET}\033[K" \
          "${SPIN_FRAMES[i]}" "$((SECONDS - start))" "$limit"
        i=$(((i + 1) % ${#SPIN_FRAMES[@]}))
      fi
      n=$((n + 1))
      sleep 0.1
    done
    [ -t 1 ] || printf "."
  done
  [ -t 1 ] && printf "\r\033[K"
  printf "\n"
  return 1
}

# ---------- 回滚模式 ----------
if [ "$DO_ROLLBACK" -eq 1 ]; then
  title "回滚到上一次升级前的状态"
  [ -f "$BACKUP_DIR/latest" ] || die "没有可用的备份记录"
  latest="$(cat "$BACKUP_DIR/latest")"

  if [ -f "$latest/commit.txt" ] && git -C "$SCRIPT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    commit="$(cat "$latest/commit.txt")"
    if [ -n "$commit" ]; then
      info "回退代码到 ${commit:0:8}"
      git -C "$SCRIPT_DIR" checkout -q "$commit" || warn "代码回退失败，仅恢复数据"
    fi
  fi

  restore_backup "$latest"
  info "重建并启动"
  compose_up leyun
  if wait_healthy; then
    ok "回滚完成，服务已恢复"
    if [ "$(git -C "$SCRIPT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null)" = "HEAD" ]; then
      warn "代码现在停在固定提交上，后续 ./update.sh 不会再拉取新代码"
      echo "    恢复跟随分支：git checkout <分支名>"
    fi
  else
    die "回滚后服务仍未就绪，请查看：${DC[*]} logs -f leyun"
  fi
  exit 0
fi

# ---------- 正常升级 ----------
printf "\n${C_BOLD}乐云企业网盘 · 升级${C_RESET}\n"

OLD_COMMIT=""
if git -C "$SCRIPT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
  OLD_COMMIT="$(git -C "$SCRIPT_DIR" rev-parse HEAD 2>/dev/null || true)"
fi

# 1. 拉代码
if [ "$DO_PULL" -eq 1 ]; then
  title "1/4 拉取最新代码"
  if ! git -C "$SCRIPT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    warn "当前目录不是 git 仓库，跳过拉取"
  elif [ "$(git -C "$SCRIPT_DIR" rev-parse --abbrev-ref HEAD)" = "HEAD" ]; then
    # 回滚之后代码停在某个提交上（游离 HEAD），这时 pull 没有意义。
    warn "当前代码停在固定提交上（可能是回滚留下的），跳过拉取"
    echo "    想继续跟随分支更新：git checkout <分支名>"
  elif [ -n "$(git -C "$SCRIPT_DIR" status --porcelain --untracked-files=no)" ]; then
    # 本地改动直接 pull 会冲突，停下来让人决定，别替用户丢改动。
    warn "工作区有未提交的改动，跳过拉取（用 --no-pull 可静默跳过）"
    git -C "$SCRIPT_DIR" status --short --untracked-files=no | head -10
  else
    branch="$(git -C "$SCRIPT_DIR" rev-parse --abbrev-ref HEAD)"
    info "分支 $branch"
    # 网络抖动重试三次，内网拉 GitHub 经常一次不成。
    pulled=0
    for attempt in 1 2 3; do
      # 同样不接管道：git 拉大仓库时自己有进度条，接了管道就没有了。
      if git -C "$SCRIPT_DIR" pull --ff-only; then
        pulled=1
        break
      fi
      warn "第 $attempt 次拉取失败，${attempt}0 秒后重试"
      sleep $((attempt * 10))
    done
    [ "$pulled" -eq 1 ] || warn "拉取失败，改用当前工作区继续"
  fi

  NEW_COMMIT="$(git -C "$SCRIPT_DIR" rev-parse HEAD 2>/dev/null || true)"
  if [ -n "$OLD_COMMIT" ] && [ "$OLD_COMMIT" = "$NEW_COMMIT" ]; then
    ok "代码已是最新（${NEW_COMMIT:0:8}）"
  elif [ -n "$NEW_COMMIT" ]; then
    ok "已更新到 ${NEW_COMMIT:0:8}"
    git -C "$SCRIPT_DIR" --no-pager log --oneline "${OLD_COMMIT}..${NEW_COMMIT}" 2>/dev/null |
      head -10 | sed 's/^/    /' || true
  fi
else
  title "1/4 跳过拉取代码"
fi

# 2. 备份
if [ "$DO_BACKUP" -eq 1 ]; then
  title "2/4 备份数据"
  make_backup
else
  title "2/4 跳过备份"
  warn "未备份，升级失败将无法自动回滚"
fi

# 3. 重建
title "3/4 重建镜像并重启（首次或依赖有变动时需要几分钟）"
# 这里不接管道、不做缩进，让 docker 自己往终端上画进度——见 compose_up 的注释。
if ! compose_up; then
  warn "构建失败"
  if [ "$DO_BACKUP" -eq 1 ] && [ -f "$BACKUP_DIR/latest" ]; then
    restore_backup "$(cat "$BACKUP_DIR/latest")"
    spin_run "回退到升级前的容器" "${DC[@]}" up -d leyun || true
  fi
  die "升级已中止，数据保持升级前状态"
fi
ok "容器已重建"

# 4. 健康检查
title "4/4 健康检查"
if ! wait_healthy; then
  warn "服务未在两分钟内就绪，开始自动回滚"
  if [ "$DO_BACKUP" -eq 1 ] && [ -f "$BACKUP_DIR/latest" ]; then
    latest="$(cat "$BACKUP_DIR/latest")"
    if [ -n "$OLD_COMMIT" ]; then
      git -C "$SCRIPT_DIR" checkout -q "$OLD_COMMIT" 2>/dev/null || true
    fi
    restore_backup "$latest"
    # 回滚这一步照样把 docker 的输出放出来：这时候人正盯着屏幕想知道
    # 到底怎么了，闷着头重建只会更慌。
    compose_up leyun || true
    if wait_healthy; then
      ok "已回滚到升级前的版本"
    else
      die "回滚后仍未就绪，请手工排查：${DC[*]} logs -f leyun"
    fi
  fi
  die "升级失败"
fi

# ---------- 收尾 ----------
VERSION="$(docker exec leyun /app/leyun -version 2>/dev/null || echo '')"
printf "\n${C_GREEN}${C_BOLD}升级完成${C_RESET}\n"
printf "${C_DIM}%s${C_RESET}\n" "────────────────────────────────────────────────────────"
[ -n "$VERSION" ] && printf "  版本      %s\n" "$VERSION"
printf "  访问地址  http://%s:%s\n" "$(hostname -I 2>/dev/null | awk '{print $1}' || echo localhost)" "$PORT"
[ "$DO_BACKUP" -eq 1 ] && printf "  备份      %s\n" "$(basename "$(cat "$BACKUP_DIR/latest")")"
printf "${C_DIM}%s${C_RESET}\n" "────────────────────────────────────────────────────────"
printf "  如有异常可回滚：./update.sh --rollback\n\n"
