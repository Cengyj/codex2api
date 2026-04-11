# 部署说明

## 当前服务面

部署后主要使用以下入口：

- 管理台：`/admin/`
- 健康检查：`/health`
- 管理 API：`/api/admin/*`

## 本地启动

```bash
cp .env.example .env
# ?? .env????? ADMIN_SECRET
cd frontend && npm ci && npm run build && cd ..
go run .
```

> `ADMIN_SECRET` ??????????????????????

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
