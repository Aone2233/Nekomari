# 部署

生产部署的形状是：**面板只监听回环 → 反向代理终止 TLS → Cloudflare 或直连**。

```
Cloudflare ──▶ nginx :443 ──▶ 127.0.0.1:25774 (面板容器)
                  │
                  └── 证书、真实来源 IP、WebSocket 透传
```

面板容器**不建议**直接暴露到公网。

## 反向代理的两个硬性要求

### 1. 透传 WebSocket 升级头

探针持有长连接。缺这两行，连接会被代理掐断，表现为面板上节点**反复上下线**：

```nginx
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
```

并且**读超时要放宽**。探针空闲时连接会保持很久，nginx 默认的 60s 会把它关掉：

```nginx
proxy_read_timeout 3600s;
proxy_send_timeout 3600s;
```

### 2. 只信任来自 Cloudflare 的 `CF-Connecting-IP`

这一条比看起来重要。源站的 443 通常对公网开放，**任何知道源站 IP 的人都能直接连上并随手写一个 `CF-Connecting-IP`**。无条件采信它的后果不只是"IP 记错"：

- 面板的**登录限流按 IP 计数** —— 每次换一个伪造头就等于换一个身份，按 IP 那一维完全失效，可以无限试密码与 2FA
- 会话记录与审计日志里写入的是攻击者指定的地址

正确做法是先用 `geo` 判断来源是否真的属于 Cloudflare，是才采信：

```nginx
geo $trust_cf {
    default 0;
    include /etc/nginx/cloudflare-ips.conf;   # 取自 Cloudflare 官方公开列表
}
```

!!! warning "这一层挡不住「直连源站」本身"
    它挡的是**伪造来源**。要彻底关掉直连，还需要在防火墙或安全组上把 443 限制到同一份 Cloudflare 网段。两者互补，见仓库的 `deploy/nginx-nekomari.conf` 与 `deploy/README.md`。

## 一个可用的 vhost

完整体（含 geo 段与证书路径）见仓库的 `deploy/nginx-nekomari.conf`。骨架：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name panel.example.com;
    return 301 https://$host$request_uri;      # 80 只做跳转，TLS 一律在 443
}

server {
    listen 443 ssl http2;
    server_name panel.example.com;

    ssl_certificate     /etc/nginx/ssl/fullchain.pem;
    ssl_certificate_key /etc/nginx/ssl/privkey.pem;

    # WebSocket：探针长连接必需
    proxy_http_version 1.1;
    proxy_set_header Upgrade    $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;

    # 真实来源 IP（仅在确实来自 Cloudflare 时采信）
    proxy_set_header X-Real-IP        $remote_addr;
    proxy_set_header X-Forwarded-For  $proxy_add_x_forwarded_for;
    proxy_set_header Host             $host;

    location / {
        proxy_pass http://127.0.0.1:25774;
    }
}
```

## 证书

用 **Cloudflare Origin 证书**最省事：有效期长、不需要续期任务、和 Cloudflare 代理配合不会有"重定向次数过多"的问题。

!!! note "Origin 证书只在经过 Cloudflare 代理时可信"
    它由 Cloudflare 自己的 CA 签发，浏览器不认。所以**必须**开着 Cloudflare 代理。如果改成 DNS-only 直连，浏览器会报证书错误 —— 那时需要换成公开 CA 签发的证书。

## 数据与备份

面板的全部状态在数据目录（容器内 `/app/data`）：SQLite 数据库、主题、插件。

```bash
# 备份
docker run --rm -v nekomari-data:/data -v "$PWD:/backup" alpine \
  tar czf /backup/nekomari-$(date +%F).tgz -C /data .

# 升级前留档（推荐每次都做）
cp -a /opt/nekomari/data /opt/nekomari-backups/pre-$(date +%Y%m%d-%H%M%S)
```

备份里含 token 与密钥，按密钥保管。

## 被监控主机的防火墙

**不需要为探针开放任何入站端口。** 上报与 WebSocket 都是探针主动连出，出口方向能到面板的 443 即可。

## 下一步

- [探针](agent.md) —— 参数、自动发现、升级
- [升级](upgrading.md) —— 面板与探针各自的升级路径
