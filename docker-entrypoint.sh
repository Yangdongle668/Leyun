#!/bin/sh
#
# 容器入口：先以 root 把数据目录的属主理顺，再降权成 leyun 跑正式进程。
#
# 为什么需要这一步——
# compose 把宿主机的 ./data 挂到 /app/data。挂载会连宿主机那个目录的属主
# 一起带进来，把镜像里 chown 好的盖掉。宿主机上它通常是 root 建的
# （用 root 跑 deploy.sh 或 docker compose 都会这样），而容器里进程是
# uid 1000，于是写不进去，SQLite 报：
#
#     unable to open database file: out of memory (14)
#
# 字面像内存不够，其实是 SQLITE_CANTOPEN。与其让每个部署的人都踩一遍，
# 不如启动时自己修掉。
set -e

DATA_DIR=/app/data

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR"

  # 只在属主确实不对时才 chown。文件多了之后递归 chown 很慢，
  # 每次重启都跑一遍没有道理。
  if [ "$(stat -c '%u' "$DATA_DIR")" != "$(id -u leyun)" ]; then
    echo "[entrypoint] 数据目录属主不是 leyun，正在修正：$DATA_DIR"
    chown -R leyun:leyun "$DATA_DIR"
  fi

  # 立刻降权。正式进程始终以非 root 身份运行，root 只用来做上面这一下。
  exec su-exec leyun:leyun /app/leyun "$@"
fi

# 已经是非 root 启动的（compose 里显式写了 user:），那就直接跑，
# 属主由外面自己负责——这种情况下我们也没有权限去改。
exec /app/leyun "$@"
