# 对象存储密钥配置说明（Provider + Drive）

> **给谁看：** 要接入自己云存储的用户 / 运维 / 对接 Agent 的开发者。  
> **一句话：** 在控制台拿到 AccessKey + SecretKey，通过 API 建成 **Provider**，再建 **Drive** 指定桶名。  
> **相关文档：** [VENDORS.md](./VENDORS.md)（厂商列表）· [CLOUD-INTEGRATION.md](./CLOUD-INTEGRATION.md)（STS 进阶）· [PRODUCTION.md](./PRODUCTION.md)（MASTER_KEY）

---

## 0. 先搞懂三个概念

| 名称 | 是什么 | 里面有什么 |
|------|--------|------------|
| **Provider（云账号）** | 某一云厂商的一套登录密钥 | `access_key` + `secret_key` + `endpoint` 等 |
| **Drive（逻辑盘）** | 某个桶里的一块工作区 | `bucket`、可选 `prefix`、`mount_point` |
| **Session（短时会话）** | hubd/runner 挂盘时用的临时配置 | 由控制面根据 Provider 生成，**不是**你手动贴 Key |

```text
云厂商控制台
  → 复制 AK / SK
  → POST /v1/providers   （存密钥）
  → POST /v1/drives      （选桶）
  → hubd / runner 申请 session 挂盘
  → 文件直写你的桶（不经控制面中转 body）
```

**常见误解：**

| 误解 | 正确理解 |
|------|----------|
| 把腾讯云「API 密钥」随便塞进 COS 就能用 | 要用**有 COS 权限**的 SecretId/SecretKey；DNS 密钥 ≠ COS 密钥 |
| Key 写在 Drive 里 | Key 只写在 **Provider.credentials** |
| 配完 Key 就能在浏览器里当网盘 | 这是 **API 控制面**；浏览器看状态页，读写靠 Agent/hubd/runner |

---

## 1. 准备工作

### 1.1 API 地址

| 环境 | 示例 |
|------|------|
| 线上（本项目现网） | `https://sstc.chat` |
| 本地开发 | `http://127.0.0.1:8080` 或 `http://127.0.0.1:18080` |

下文统一用：

```bash
export API=https://sstc.chat   # 按你的环境改
```

### 1.2 登录拿 Token

```bash
export TOKEN=$(curl -sS -X POST "$API/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"你的用户名","password":"你的密码"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "token ok, len=${#TOKEN}"
```

Agent 场景：用人账号建好盘后，再发 **Agent Token**（需 `provider.read` / `drive.write` 等 scope），见 [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md)。

### 1.3 查看系统支持哪些厂商

```bash
curl -sS "$API/v1/providers/catalog" \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

返回里每个 `type` 都有 `fields`（必填字段）和 `notes`。

### 1.4 生产安全（强烈建议）

| 项 | 说明 |
|----|------|
| `AI_CLOUDHUB_MASTER_KEY` | API 进程环境变量；开启后密钥**加密**存库 |
| 最小权限 AK | 只给「某一个桶」的读写，不要用主账号 Root Key |
| 不要把 SK 写进前端 / Git | 只通过 HTTPS + 登录后的 API 提交 |

---

## 2. 标准两步配置（所有厂商通用）

### 步骤 A — 创建 Provider（存 Key）

```http
POST /v1/providers
Authorization: Bearer <TOKEN>
Content-Type: application/json
```

```json
{
  "name": "起一个好记的名字",
  "type": "r2",
  "credentials": {
    "access_key": "……",
    "secret_key": "……",
    "account_id": "……"
  }
}
```

记下返回的 **`id`**，后面叫 `$PID`。

### 步骤 B — 创建 Drive（指定桶）

```http
POST /v1/drives
Authorization: Bearer <TOKEN>
Content-Type: application/json
```

```json
{
  "name": "workspace",
  "provider_id": "<上一步的 PID>",
  "bucket": "你的桶名",
  "prefix": "可选/子目录/",
  "mount_point": "/workspace"
}
```

| 字段 | 必填 | 含义 |
|------|------|------|
| `provider_id` | 是 | 哪套云密钥 |
| `bucket` | 是 | 存储桶名称 |
| `prefix` | 否 | 只把桶下某个前缀当工作区 |
| `mount_point` | 建议 | 本机挂载路径（hubd/runner 用） |

### 步骤 C — 验证（可选）

```bash
# 探测密钥能否 ListBuckets（权限不足可能失败，但指定桶仍可能可用）
curl -sS -X POST "$API/v1/providers/$PID/health" \
  -H "Authorization: Bearer $TOKEN"

# 申请挂盘会话（看 source / note）
curl -sS -X POST "$API/v1/drives/$DID/session" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"device_id":"docs-1","mode":"sync_workspace"}' | python3 -m json.tool
```

`session.source`：

| 值 | 含义 |
|----|------|
| `embedded` | 用你配置的长期 AK/SK 生成短时 conf（**默认就能用**） |
| `aliyun_sts` / `tencent_sts` / … | 开启了云 STS 且成功 |
| 带 `note` | STS 失败时的说明；仍可能回退 `embedded` |

---

## 3. 各厂商：控制台拿什么 → 填什么

### 3.1 字段对照总表

| `type` | 厂商 | 控制台里的名字（常见） | 必填 credentials |
|--------|------|------------------------|------------------|
| `r2` | Cloudflare R2 | Access Key ID / Secret Access Key / Account ID | `access_key`, `secret_key`, `account_id` |
| `minio` | MinIO | Access Key / Secret Key / API 地址 | `access_key`, `secret_key`, `endpoint` |
| `s3` | AWS S3 或通用 S3 | Access key ID / Secret / endpoint | `access_key`, `secret_key`, `endpoint`, 建议 `region` |
| `oss` | 阿里云 OSS | AccessKey ID / AccessKey Secret / Endpoint | `access_key`, `secret_key`, `endpoint` |
| `cos` | 腾讯云 COS | SecretId / SecretKey / 地域 Endpoint | `access_key`, `secret_key`, `endpoint` |
| `b2` | Backblaze B2 | keyID / applicationKey / S3 endpoint | `access_key`, `secret_key`, `endpoint` |
| `qiniu` | 七牛 Kodo | AccessKey / SecretKey / S3 域名 | `access_key`, `secret_key`, `endpoint` |
| `oracle` | Oracle OCI | Customer Secret 的 Access/Secret + S3 兼容 endpoint | `access_key`, `secret_key`, `endpoint` |

---

### 3.2 Cloudflare R2（推荐新手）

**在 Cloudflare 控制台：**

1. R2 → Manage R2 API Tokens → 创建 Token  
2. 复制 **Access Key ID**、**Secret Access Key**  
3. 右侧或账户首页复制 **Account ID**  
4. 先建好一个 Bucket（桶）

**创建 Provider：**

```bash
curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "cloudflare-r2",
    "type": "r2",
    "credentials": {
      "access_key": "粘贴 Access Key ID",
      "secret_key": "粘贴 Secret Access Key",
      "account_id": "粘贴 Account ID"
    }
  }'
```

**创建 Drive（把 `my-bucket` 换成真实桶名）：**

```bash
curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "r2-workspace",
    "provider_id": "'"$PID"'",
    "bucket": "my-bucket",
    "prefix": "workspace/",
    "mount_point": "/workspace"
  }'
```

---

### 3.3 MinIO（自建 / 内网演示）

```bash
curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "minio-local",
    "type": "minio",
    "credentials": {
      "access_key": "minioadmin",
      "secret_key": "minioadmin",
      "endpoint": "http://127.0.0.1:9000"
    }
  }'
```

- `endpoint` 必须是 **hubd/runner 能访问到的地址**（本机 MinIO 用 `127.0.0.1`；云上 MinIO 用内网或公网 URL）。  
- 控制面在服务器上时，`127.0.0.1:9000` 是**服务器本机**，不是你笔记本。

---

### 3.4 阿里云 OSS

**控制台：** 访问控制 RAM → 用户 → 创建 AccessKey（建议只授权目标桶）。  
**Endpoint 示例：** `oss-cn-hangzhou.aliyuncs.com`（按区域改）。

```bash
curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "aliyun-oss",
    "type": "oss",
    "credentials": {
      "access_key": "LTAI5t……",
      "secret_key": "……",
      "endpoint": "oss-cn-hangzhou.aliyuncs.com",
      "region": "cn-hangzhou"
    }
  }'
```

可选短时凭证（API **进程环境变量**，不是 JSON 字段）：

```bash
# 写在跑 api 的机器环境里，不是用户 curl 里
export AI_CLOUDHUB_OSS_NATIVE_STS=1
export AI_CLOUDHUB_OSS_STS_ROLE_ARN='acs:ram::账号ID:role/角色名'
```

详见 [CLOUD-INTEGRATION.md §1](./CLOUD-INTEGRATION.md)。

---

### 3.5 腾讯云 COS

**控制台：** 访问管理 CAM → API 密钥（SecretId / SecretKey）。  
**Endpoint 示例：** `cos.ap-guangzhou.myqcloud.com`。  
**桶名注意：** 经常是 `桶名-AppId`，例如 `mybucket-1307370647`。

```bash
curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "tencent-cos",
    "type": "cos",
    "credentials": {
      "access_key": "AKID……",
      "secret_key": "……",
      "endpoint": "cos.ap-guangzhou.myqcloud.com",
      "region": "ap-guangzhou"
    }
  }'
```

**和 DNS 密钥的区别：**

| 密钥 | 用途 |
|------|------|
| DNSPod / 域名解析用的 SecretId | 改 DNS，**不能**代替 COS 读写 |
| 有 **COS 权限** 的 SecretId/SecretKey | 才能配 `type=cos` 的 Provider |

本仓库运维可能用 `~/key/SecretKey.csv` 管云 API；**配存储桶时请用有 COS 权限的密钥**，不要混用。

可选 STS：见 [CLOUD-INTEGRATION.md §2](./CLOUD-INTEGRATION.md)。

---

### 3.6 通用 S3 / Backblaze B2 / 七牛 / Oracle

**AWS 或兼容站 (`type=s3`)：**

```json
{
  "name": "aws-or-compat",
  "type": "s3",
  "credentials": {
    "access_key": "…",
    "secret_key": "…",
    "endpoint": "s3.amazonaws.com",
    "region": "us-east-1"
  }
}
```

**B2 (`type=b2`)：** endpoint 形如 `s3.us-west-000.backblazeb2.com`。

**七牛 (`type=qiniu`)：** 挂盘用 S3 域名，如 `s3-cn-east-1.qiniucs.com`。

**Oracle (`type=oracle`)：** 使用 **Customer Secret Keys** +  
`namespace.compat.objectstorage.region.oraclecloud.com`。

更多字段与 STS： [CLOUD-INTEGRATION.md](./CLOUD-INTEGRATION.md)。

---

## 4. 完整示例（复制改值即可）

以 **R2** 为例，从登录到 Drive：

```bash
export API=https://sstc.chat

TOKEN=$(curl -sS -X POST "$API/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"你的密码"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

PID=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "my-r2",
    "type": "r2",
    "credentials": {
      "access_key": "替换",
      "secret_key": "替换",
      "account_id": "替换"
    }
  }' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "provider_id=$PID"

DID=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{
    \"name\": \"ws\",
    \"provider_id\": \"$PID\",
    \"bucket\": \"替换成桶名\",
    \"prefix\": \"agent/\",
    \"mount_point\": \"/workspace\"
  }" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "drive_id=$DID"
```

列表确认：

```bash
curl -sS "$API/v1/providers" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
curl -sS "$API/v1/drives" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

---

## 5. 配好之后怎么用

| 场景 | 做法 |
|------|------|
| 本机挂盘 | [HUBD.md](./HUBD.md)：`hubd` + binding `desired=mounted` |
| 无 FUSE | `mode=sync_workspace` 或 `make demo-local`（本地演示） |
| Agent | [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md)：Agent Token + MCP |
| 跑任务 | [RUNNER.md](./RUNNER.md) / [JOBS.md](./JOBS.md) |

环境变量：

```bash
export AI_CLOUDHUB_API=https://sstc.chat
export AI_CLOUDHUB_TOKEN='人的token或Agent token'
```

---

## 6. 常见错误

| 现象 | 可能原因 | 处理 |
|------|----------|------|
| 创建 Provider 400 | 缺字段 / type 写错 | 对照 catalog 的 `fields` |
| health 502 | 密钥错、endpoint 错、网络不通 | 控制台重拷密钥；检查 endpoint 是否带 `https://` 或区域 |
| session `embedded` + note 一长串 | 未开 STS 或 STS 失败 | 默认可用；要 STS 再配 RoleArn 环境变量 |
| hubd 挂不上桶 | 桶名错、权限不足、endpoint 对控制面通但对本机不通 | Drive.bucket；CAM/RAM 策略；本机网络 |
| 腾讯 COS 403 | 桶名未带 AppId、密钥无 COS 权限 | 用 `name-appid`；换有权限的密钥 |
| 把 DNS 密钥当 COS 密钥 | 用途不同 | 单独建 COS 密钥 |

---

## 7. API 速查

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/providers/catalog` | 厂商与字段说明 |
| GET | `/v1/providers` | 我的云账号列表（密钥不会以明文回显） |
| POST | `/v1/providers` | 新建（提交 credentials） |
| POST | `/v1/providers/{id}/health` | 探测连通 |
| DELETE | `/v1/providers/{id}` | 删除 |
| GET/POST | `/v1/drives` | 逻辑盘 |
| POST | `/v1/drives/{id}/session` | 挂盘会话 |

OpenAPI：`docs/openapi.yaml`。

---

## 8. 文档导航

| 文档 | 内容 |
|------|------|
| **本文 PROVIDERS.md** | **怎么配各云 Key（入门必读）** |
| [VENDORS.md](./VENDORS.md) | 厂商选型与优先级 |
| [CLOUD-INTEGRATION.md](./CLOUD-INTEGRATION.md) | OSS/COS/七牛/OCI 与 STS 细表 |
| [STS.md](./STS.md) / [STS-RUNBOOK.md](./STS-RUNBOOK.md) | 短时凭证原理与联调 |
| [HUBD.md](./HUBD.md) / [RUNNER.md](./RUNNER.md) | 本机挂盘与 Job |
| [PRODUCTION.md](./PRODUCTION.md) | MASTER_KEY、STRICT |

---

**记住三步：控制台拿 AK/SK → `POST /v1/providers` → `POST /v1/drives` 填 bucket。**
