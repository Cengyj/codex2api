# Codex2API

Codex2API 当前定位为 **账号管理后台**，聚焦以下能力：

- 账号导入 / 导出 / 迁移
- 刷新账号状态
- 刷新 AT / 测试连接
- 代理管理（文件自带代理 / 动态代理 / 代理池 / 全局代理）
- 动态代理自动分配、失败切换、定时轮换
- CPA Sync
- 管理台系统设置与运维面板

> 当前运行面只保留管理后台与管理 API；旧的公共模型中转接口与 API Key 管理面已清理出产品表面。

## 当前入口

- 管理台：`/admin/`
- 健康检查：`/health`
- 管理 API：`/api/admin/*`

## 快速启动

### 本地开发

```bash
cp .env.example .env
# 修改 .env，并设置 ADMIN_SECRET
cd frontend && npm ci && npm run build && cd ..
go run .
```

> 自 2026-04-11 起，`ADMIN_SECRET` 为启动必填；若环境变量和数据库中都为空，服务会启动失败。

启动后访问：

- `http://localhost:8080/admin/`
- `http://localhost:8080/health`

### 前端联调

```bash
cd frontend
npm ci
npm run dev
```

前端开发服务默认通过 Vite 代理管理 API 与健康检查。

## 当前保留的核心功能

### 账号管理

- Refresh Token / Access Token 导入
- TXT / JSON 文件导入
- 去重、批量导入、热加载
- 导出 / 迁移
- 手动刷新 / 批量测试 / 风险清理

### 代理系统

- 文件自带代理优先
- 动态代理 URL 按需获取
- 动态代理失效自动切换
- 24 小时轮换代理
- 代理池兜底
- 全局代理兜底

### 刷新与后台任务

- 启动后自动刷新
- 后台定时刷新状态
- 用量探测 / 恢复探测
- 自动清理异常账号
- CPA Sync 定时同步

## 文档

- [文档索引](docs/README.md)
- [管理 API 概览](docs/API.md)
- [部署说明](docs/DEPLOYMENT.md)
- [架构说明](docs/ARCHITECTURE.md)
- [故障排查](docs/TROUBLESHOOTING.md)
- [配置说明](docs/CONFIGURATION.md)

## 开发说明

前端位于 `frontend/`，后端核心入口位于：

- `main.go`
- `admin/handler.go`
- `auth/store.go`
- `auth/proxy_manager.go`

如果调整账号管理逻辑，请优先保证以下边界不被破坏：

- 账号导入/导出/迁移
- 刷新状态 / 刷新 AT / 测试连接
- 代理管理与动态代理分配
- CPA Sync
- 管理台登录与系统设置
