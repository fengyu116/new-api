# 二开发布流程：GitHub 构建镜像，服务器只拉镜像

这套流程用来减少每次二开的麻烦：

```text
本地改代码 -> push 到 GitHub dev/custom -> GitHub Actions 自动构建镜像
  -> VPS docker compose pull new-api -> docker compose up -d
```

服务器不再源码构建，所以内存压力会小很多。Redis 和 PostgreSQL 继续由 Docker Compose 管理，不需要手动安装。

## 第一次准备

1. GitHub 仓库使用你的 fork，并保留 `dev/custom` 分支。
2. GitHub 打开 `Actions`，确认工作流可运行。
3. 推送一次 `dev/custom` 后，等待 `Build Custom Docker Image` 运行成功。
4. 镜像地址格式是：

```text
ghcr.io/你的github用户名/new-api:custom
```

如果 GitHub Package 默认是私有，服务器需要先登录：

```bash
docker login ghcr.io -u 你的github用户名
```

密码填写 GitHub Personal Access Token，需要有 `read:packages` 权限。

## 服务器第一次改 Compose

在服务器 `/opt/new-api` 里放两份文件：

```bash
cp deploy/docker-compose.custom.yml docker-compose.yml
cp deploy/.env.custom.example .env
nano .env
```

`.env` 至少要改这些：

```text
NEW_API_IMAGE=ghcr.io/你的github用户名/new-api:custom
POSTGRES_PASSWORD=很长的随机密码
REDIS_PASSWORD=很长的随机密码
SESSION_SECRET=很长的随机字符串
```

启动：

```bash
cd /opt/new-api
docker compose pull
docker compose up -d
docker compose ps
curl http://127.0.0.1:3000/api/status
```

## 以后改代码发布

本地 PowerShell：

```powershell
cd C:\Users\fengyu\Desktop\docker_project\new-api
git status
git add .
git commit -m "你的修改说明"
git push origin dev/custom
```

去 GitHub 的 `Actions` 页面，等 `Build Custom Docker Image` 绿色成功。

然后本地执行服务器更新：

```powershell
$env:REMOTE_SSH_PASSWORD="你的服务器root密码"
.\scripts\deploy_custom_remote.ps1
```

如果要同时更新模型目录数据：

```powershell
$env:FZBL_UPSTREAM_KEY="你的上游key"
$env:REMOTE_SSH_PASSWORD="你的服务器root密码"
.\scripts\release_custom.ps1 -GenerateSql -Deploy -SkipTests
```

更稳一点的完整发布：

```powershell
$env:FZBL_UPSTREAM_KEY="你的上游key"
$env:REMOTE_SSH_PASSWORD="你的服务器root密码"
.\scripts\release_custom.ps1 -GenerateSql -Deploy
```

## 只改模型数据，不改代码

```powershell
cd C:\Users\fengyu\Desktop\docker_project\new-api
$env:FZBL_UPSTREAM_KEY="你的上游key"
python scripts\import_fzbl_catalog.py --fzbl ..\fzbl.txt --cank ..\cank.txt --base-url https://q.aibaotui.com --credit-unit-price 0.05 --out-dir tmp\fzbl-import

$env:REMOTE_SSH_PASSWORD="你的服务器root密码"
node scripts\remote_apply_sql.js --sql tmp\fzbl-import\fzbl-import.sql --host 154.12.60.218 --user root --backup
```

## 不要做的事

不要执行：

```bash
docker compose down -v
```

这个会删除数据库卷，可能导致线上数据丢失。

不要把服务器密码、上游 key、GitHub token 写进代码或提交到 GitHub。
