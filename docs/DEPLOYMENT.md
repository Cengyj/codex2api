# 部署说明

## 当前服务面

部署后主要使用以下入口：

- 管理台：`/admin/`
- 健康检查：`/health`
- 管理 API：`/api/admin/*`

## 本地启动

```bash
cp .env.example .env
# 修改 .env，并设置 ADMIN_SECRET
cd frontend && npm ci && npm run build && cd ..
go run .
```

> `ADMIN_SECRET` 现在是启动必填项，缺失时服务不会启动。

## Docker 启动

```bash
docker compose pull
docker compose up -d
docker compose logs -f codex2api
```

## 反向代理建议

```nginx
server {
    listen 80;
    server_name your-domain;

    location /admin/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /api/admin/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /health {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
}
```

## 升级建议

升级前建议：

1. 备份数据库
2. 先构建前端
3. 再重启后端
4. 升级后检查 `/health` 与 `/admin/`

## 验证清单

- `/health` 返回 200
- `/admin/` 可打开
- 能正常登录管理台
- 账号列表可加载
- 代理设置可保存
- 测试连接 / 刷新状态正常

## 2026-04-11 更新说明：保留旧 PostgreSQL / Redis 只更新应用容器

如果你的服务器是：

- 先 `git pull` 拉最新源码
- 继续复用旧 PostgreSQL / Redis
- 只更新 `codex2api` 应用容器

那么不要只执行：

```bash
docker compose pull codex2api
```

因为这只会拉镜像，不会把刚 `git pull` 下来的前后端源码重新构建进容器，结果就会出现“后端代码更新了，但 UI 还是旧版本”的现象。

推荐更新方式：

```bash
cd /opt/codex2api
git pull origin main

docker stop codex2api || true
docker rm codex2api || true

docker build -t ghcr.io/james-6-23/codex2api:latest .
docker compose up -d --no-deps codex2api

docker logs --tail=100 -f codex2api
curl http://127.0.0.1:${CODEX_PORT:-8080}/health
```

### 外部 1Panel PostgreSQL / Redis

如果 `.env` 中的 `DATABASE_HOST` / `REDIS_ADDR` 指向外部 1Panel 容器名，则 `codex2api` 必须加入对应外部 Docker 网络，否则容器内无法解析这些主机名。

示例 `docker-compose.override.yml`：

```yaml
services:
  codex2api:
    networks:
      - codex2api-net
      - panel_net

networks:
  panel_net:
    external: true
    name: 1panel-network
```

这只是在网络层连通旧中间件，不会替换或重建你原来的数据库和 Redis。
