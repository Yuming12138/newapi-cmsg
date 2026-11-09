# 注册入口与可信代理

注册接口 `/api/user/register` 使用独立的滑动窗口限流。默认每个客户端 IP
10 分钟最多 5 次尝试，口令错误和其他校验失败也计数；登录与密码重置继续
使用原有的 CriticalRateLimit。Redis 更新通过 Lua 原子完成；Redis 不可用时
注册返回 503。没有启用 Redis 的部署使用进程内计数。

## 应用部署配置

~~~yaml
environment:
  TRUSTED_PROXIES: "172.18.0.1/32"
  REGISTRATION_RATE_LIMIT: "5"
  REGISTRATION_RATE_LIMIT_DURATION: "600"
~~~

`TRUSTED_PROXIES` 是逗号分隔的代理 IP/CIDR 列表。为空时忽略全部转发 IP
头，使用连接来源地址；无效配置或 `/0` 会使服务启动失败。仅处理来自可信
代理的 `X-Forwarded-For`。上述地址是 cmsg-root 的 Docker bridge 网关快照，
部署前通过 `docker inspect new-api` 确认，不能直接套用到其他环境。

应用端口保持绑定宿主 `127.0.0.1:3000`。每个可信代理都必须先核实来源，
并覆盖客户端提交的转发头。当前公网 Nginx 使用：

~~~nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $remote_addr;
~~~

CDN 接入前先为 Nginx 配置官方代理地址范围与真实 IP 解析，再验证限流使用
客户端地址。公网请求不能直接选择自己的限流身份。

## Nginx 注册专用限制

在 `http` 上下文定义共享区：

~~~nginx
limit_req_zone $binary_remote_addr zone=newapi_register:1m rate=1r/m;
~~~

在 New API 的 HTTPS server 中添加精确 location，保留通用 location 的代理
行为与上游地址：

~~~nginx
location = /api/user/register {
    limit_req zone=newapi_register burst=3 nodelay;
    limit_req_status 429;
    client_max_body_size 512k;
    proxy_pass http://127.0.0.1:3000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_cache off;
}
~~~

修改前保存配置备份；`nginx -t` 成功后平滑 reload。配置新应用环境变量需要
重建 New API 容器，先按 DEVELOPMENT.md 构建、发布和核验静态二进制。

## 口令轮换与开放注册

管理后台“认证设置 / Basic Authentication”中的 RegistrationCodes 是完整
允许列表。用新生成的随机口令替换所有旧值，RegistrationCodeEnabled 保持
true，通过 `PUT /api/option/` 保存以同步内存配置。口令保存在受保护位置，
Git、聊天、命令参数和审计记录只保存非敏感状态。

轮换和验证期间 RegisterEnabled 保持 false。服务端测试覆盖旧口令失效、
新口令校验、并发限流、窗口到期、独立 IP、转发头伪造与 Redis 故障。现网
用注册关闭状态验证限流和客户端 IP，避免为测试创建用户或开放注册。

开放注册前建议配置并验证 Cloudflare Turnstile。现有开关同时保护登录和
注册，启用前核对站点域名与凭据，验证正常登录。重新开放注册由管理员单独
决定。
