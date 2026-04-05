# 服务器部署与更新说明

本文档用于固定 `PostgreSQL + Redis` 线上环境的正确更新口径，避免因为 `docker-compose.local.yml` 与卷名混用而误切到错误数据库。

## 当前固定结论

- 线上继续使用 `docker-compose.local.yml`
- 线上 PostgreSQL 正确卷名：`codex2api_pgdata`
- 线上 Redis 正确卷名：`codex2api_redisdata`
- 不要再把线上环境挂到 `codex2api-local_pgdata`
- 不要再把线上环境挂到 `codex2api-local_redisdata`

说明：

- `docker-compose.local.yml` 可以继续用于本地源码容器构建
- 但如果它绑定的是 `codex2api-local_*` 卷，服务会连到另一套新卷
- 这时容器依然能正常启动，但会表现为“数据库像被清空了一样”
- 这通常不是原数据库被删掉，而是切到了错误卷

## 首次修正到正确数据库卷

当服务器当前误用了 `codex2api-local_pgdata` / `codex2api-local_redisdata` 时，先执行下面这套命令，把 `docker-compose.local.yml` 修正为复用正式卷。

```bash
cd /opt/codex2api

docker compose -f docker-compose.local.yml down

sed -i 's/name: codex2api-local/name: codex2api/' docker-compose.local.yml
sed -i 's/codex2api-local_pgdata/codex2api_pgdata/' docker-compose.local.yml
sed -i 's/codex2api-local_redisdata/codex2api_redisdata/' docker-compose.local.yml

git pull origin main

BUILD_VERSION=$(git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD) docker compose -f docker-compose.local.yml up -d --build
```

## 后续日常更新命令

当服务器已经确认绑定到正确卷后，后续更新固定使用下面这套命令即可：

```bash
cd /opt/codex2api
git pull origin main
BUILD_VERSION=$(git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD) docker compose -f docker-compose.local.yml up -d --build
```

## 更新后检查

更新完成后，建议立刻检查当前容器实际挂载的卷：

```bash
docker inspect codex2api --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}'
docker inspect codex2api-postgres --format '{{range .Mounts}}{{println .Name "->" .Destination}}{{end}}'
docker inspect codex2api-redis --format '{{range .Mounts}}{{println .Name "->" .Destination}}{{end}}'
```

期望输出：

```text
/opt/codex2api/docker-compose.local.yml
codex2api_pgdata -> /var/lib/postgresql/data
codex2api_redisdata -> /data
```

如果看到下面任意一个卷名，说明又切到了错误数据库：

```text
codex2api-local_pgdata
codex2api-local_redisdata
```

## 禁止操作

不要执行下面这些命令，否则可能真的删除持久化数据：

```bash
docker compose -f docker-compose.local.yml down -v
docker volume rm codex2api_pgdata
docker volume rm codex2api_redisdata
docker volume prune
```

## 清理之前错误卷

只有在你已经确认当前运行中的容器挂载的是：

```text
codex2api_pgdata -> /var/lib/postgresql/data
codex2api_redisdata -> /data
```

并且确认业务数据已经正常后，才可以清理之前误用的错误卷：

```bash
docker compose -f docker-compose.local.yml down
docker volume rm codex2api-local_pgdata
docker volume rm codex2api-local_redisdata
BUILD_VERSION=$(git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD) docker compose -f docker-compose.local.yml up -d --build
```

清理前建议先再次核对：

```bash
docker volume ls | grep codex2api
docker inspect codex2api-postgres --format '{{range .Mounts}}{{println .Name "->" .Destination}}{{end}}'
docker inspect codex2api-redis --format '{{range .Mounts}}{{println .Name "->" .Destination}}{{end}}'
```

如果当前挂载里仍然出现 `codex2api-local_pgdata` 或 `codex2api-local_redisdata`，不要执行删除命令。

## 额外建议

- 更新前先做一次 PostgreSQL 备份
- 不要混用 `docker-compose.yml` 与 `docker-compose.local.yml`
- 如果以后继续沿用这套线上方案，请始终把 `docker-compose.local.yml` 视为“本地构建镜像的部署文件”，而不是“本地卷名文件”
- 核心原则只有一条：线上数据永远挂正式卷 `codex2api_pgdata` / `codex2api_redisdata`
