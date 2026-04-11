# 管理 API 概览

当前服务只保留以下有效接口面：

## 健康检查

- `GET /health`

## 管理 API

统一前缀：`/api/admin/*`

启用管理鉴权时，请携带：

```http
X-Admin-Key: <ADMIN_SECRET>
```

### 账号管理

- `GET /api/admin/accounts`
- `POST /api/admin/accounts`
- `POST /api/admin/accounts/at`
- `POST /api/admin/accounts/import`
- `DELETE /api/admin/accounts/:id`
- `POST /api/admin/accounts/:id/refresh`
- `GET /api/admin/accounts/:id/test`
- `POST /api/admin/accounts/batch-test`
- `GET /api/admin/accounts/export`
- `POST /api/admin/accounts/migrate`

### 代理管理

- `GET /api/admin/proxies`
- `POST /api/admin/proxies`
- `PATCH /api/admin/proxies/:id`
- `DELETE /api/admin/proxies/:id`
- `POST /api/admin/proxies/batch-delete`
- `POST /api/admin/proxies/test`

### 设置与运维

- `GET /api/admin/settings`
- `PUT /api/admin/settings`
- `GET /api/admin/ops/overview`
- `GET /api/admin/stats`
- `GET /api/admin/health`

### CPA Sync

- `GET /api/admin/cpa-sync/status`
- `POST /api/admin/cpa-sync/run`
- `POST /api/admin/cpa-sync/switch`
- `POST /api/admin/cpa-sync/test-cpa`
- `POST /api/admin/cpa-sync/test-mihomo`
- `POST /api/admin/cpa-sync/mihomo-groups`

## 说明

- 账号导入时支持代理配置字段
- 刷新状态、刷新 AT、测试连接等流程会按当前代理优先级解析有效代理
- 上传相关流程会按既定逻辑移除不应上送的代理字段
