/* dnsfwd.c —— 本地 DNS 转发器 + 上游优选引擎（9Router 模块用）
 *
 * 背景：Android 没有 /etc/resolv.conf，Go 的解析器读不到服务器列表时会退回到
 *       localhost:53（127.0.0.1 / ::1）。本程序监听回环 53，把查询转发到上游 DNS。
 *       ⇒ 不需要 /etc/resolv.conf、不需要改 hosts、不需要重启。
 *
 * 上游文件（默认 /data/adb/9router/dns-upstreams.conf，由 9rctl 生成）：
 *       # 注释；每行一条
 *       nameserver 223.5.5.5            ← IPv4
 *       nameserver 2400:3200::1         ← IPv6
 *       （识别不了的其它行会被跳过并在 -v 下提示，便于将来扩展 doh/dot）
 *       ⚠️ 文件里**不要**出现 127.0.0.1（会自我循环）
 *
 * 用法：
 *   dnsfwd                          # 前台运行（由 service.sh / 9rctl 后台拉起）
 *   dnsfwd -f <上游文件>            # 指定上游文件
 *   dnsfwd -t <域名>                # 自检：走本程序监听端口解析一次
 *   dnsfwd -b loopback|any          # 绑定范围（默认 loopback，安全）
 *   dnsfwd -i <网卡>                # 上游 socket 绑到指定网卡（SO_BINDTODEVICE）
 *                                   #   ⇒ 绕过 TUN：拿到真实 IP（注意：TUN 的域名规则可能因此不命中）
 *   dnsfwd -P -d <域名> [-d 域名]…  # 【探测】并发测全部上游：延迟/成功率/答案/是否 fake-ip（TSV）
 *   dnsfwd -p <端口>                # 换端口（调试用）
 *   dnsfwd -h                       # 帮助
 *
 * 热重载：收到 SIGHUP 立即重读上游文件；也会每 30 秒检查文件 mtime 自动重载。
 *         ⇒ 9rctl 改完上游后让优选结果生效，无需重启进程、不中断解析。
 *
 * 编译：见同目录 build-dnsfwd.sh（静态链接，零运行库依赖）
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <pthread.h>
#include <time.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <netinet/in.h>
#include <arpa/inet.h>

/* ---- DoH / DoT（可选编译：-DWITH_DOH，静态链接 mbedTLS） ----
 * 上游文件里支持三种写法：
 *   nameserver 223.5.5.5                      明文 UDP（v4/v6）
 *   doh https://223.5.5.5/dns-query           加密 DoH（RFC 8484，POST wireformat）
 *   doh https://doh.pub/dns-query@1.12.12.12   末尾 @IP 是"自举提示"（免去先解析 DoH 域名）
 *   dot dns.alidns.com@223.5.5.5              加密 DoT（RFC 7858，853 端口）
 * 说明：@ 后是提示 IP；没有提示时用列表里第一个 UDP 上游解析该域名（并有内置兜底）。
 */
#define UP_UDP 0
#define UP_DOH 1
#define UP_DOT 2
#ifdef WITH_DOH
#include "mbedtls/ssl.h"
#include "mbedtls/error.h"
#include "mbedtls/net_sockets.h"
#include "mbedtls/entropy.h"
#include "mbedtls/ctr_drbg.h"
#include "mbedtls/x509_crt.h"
#endif

/* 上游数量上限。
   注意：内置候选池有 40+ 条（含 DoH/DoT 与 IPv6），原来写 32 会导致
   **第 33 条之后被静默丢弃**——表现为"DoH/DoT/IPv6 永远不被探测、也永远选不上"。
   故放宽到 128，并在截断时打印警告（不要再静默丢）。 */
#define MAXUP      128
#define BUFSZ      4096
#define TIMEOUT_MS 3000
#define PROBE_TIMEOUT_MS     1500   /* 明文 UDP：1.5s 足够（只用于探测，不影响转发） */
/* DoH/DoT：TLS 握手 + 查询，并发下会明显变慢（实测 dot.pub 单条已 1048ms，
   4 并发即超 1.5s 被误判为不可用）⇒ 单独放宽，避免"加密上游永远测不出来"。 */
#define PROBE_TIMEOUT_MS_TLS 5000
/* 端到端 TCP 握手（探你 API 主机是否可达）：慢一些的主机也要算"通" */
#define PROBE_TCP_TIMEOUT_MS 4000
#define MAXDOMAINS 4
#define RELOAD_CHECK_SEC 5

/* ---------------- 上游表（可热重载） ---------------- */

struct upstream {
    int type;                       /* UP_UDP / UP_DOH / UP_DOT */
    struct sockaddr_storage sa;     /* UDP：DNS 服务器；DoH/DoT：目标地址（可由提示或自举解析得到） */
    socklen_t len;
    int family;
    char text[256];                 /* 展示用：IP 或 https://…（同时作为连接池键，留足空间）*/
    char host[128];                 /* DoH/DoT 主机名（用于 SNI 与证书校验） */
    int  port;                      /* 443 / 853 */
    char path[64];                  /* DoH 路径，默认 /dns-query */
    char hint[64];                  /* @ 后的自举提示 IP（可空） */
};
static struct upstream up[MAXUP];
static int nup = 0;
static pthread_mutex_t up_lock = PTHREAD_MUTEX_INITIALIZER;

static const char *g_conf = "/data/adb/9router/dns-upstreams.conf";
static const char *g_iface = NULL;      /* -i：上游 socket 绑定的网卡（绕 TUN） */
static int g_verbose = 0;
static volatile sig_atomic_t g_reload = 0;
static volatile sig_atomic_t g_stop = 0;

static void on_hup(int sig)  { (void)sig; g_reload = 1; }
static void on_term(int sig) { (void)sig; g_stop = 1; }

/* 复制一份用于交换 */
static struct upstream tmp_up[MAXUP];
static int tmp_nup = 0;

static int parse_upstream(const char *ip, struct upstream *u)
{
    memset(u, 0, sizeof *u);
    u->family = strchr(ip, ':') ? AF_INET6 : AF_INET;
    /* 明确拒绝回环：否则会自己转发给自己，形成死循环 */
    if (u->family == AF_INET) {
        struct in_addr a;
        if (inet_pton(AF_INET, ip, &a) != 1) return 0;
        if ((ntohl(a.s_addr) >> 24) == 127) return -1;
        struct sockaddr_in *s = (struct sockaddr_in *)&u->sa;
        s->sin_family = AF_INET; s->sin_port = htons(53); s->sin_addr = a;
        u->len = sizeof(*s);
    } else {
        struct sockaddr_in6 *s = (struct sockaddr_in6 *)&u->sa;
        s->sin6_family = AF_INET6; s->sin6_port = htons(53);
        if (inet_pton(AF_INET6, ip, &s->sin6_addr) != 1) return 0;
        static const unsigned char lo[16] = {0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1};
        if (memcmp(&s->sin6_addr, lo, 16) == 0) return -1;
        u->len = sizeof(*s);
    }
    snprintf(u->text, sizeof u->text, "%s", ip);
    return 1;
}

/* 解析 doh 行：  doh https://host[:port]/path[@提示IP] */
static int parse_doh(char *s, struct upstream *u)
{
    if (!s || !*s) return 0;
    memset(u, 0, sizeof *u);
    u->type = UP_DOH;
    u->port = 443;
    snprintf(u->path, sizeof u->path, "%s", "/dns-query");
    char *at = strrchr(s, '@');
    if (at) { *at = 0; snprintf(u->hint, sizeof u->hint, "%s", at + 1); }
    char *h = s;
    if (strncmp(h, "https://", 8) == 0) h += 8;
    else if (strncmp(h, "http://", 7) == 0) h += 7;
    char *slash = strchr(h, '/');
    if (slash) { snprintf(u->path, sizeof u->path, "%s", slash); *slash = 0; }
    char *colon = strrchr(h, ':');
    if (colon && strchr(colon, ':') == colon) { u->port = atoi(colon + 1); *colon = 0; }
    snprintf(u->host, sizeof u->host, "%s", h);
    if (!u->host[0]) return 0;
    snprintf(u->text, sizeof u->text, "doh %s:%d%s", u->host, u->port, u->path);
    return 1;
}

/* 解析 dot 行：  dot host[:port][@提示IP] */
static int parse_dot(char *s, struct upstream *u)
{
    if (!s || !*s) return 0;
    memset(u, 0, sizeof *u);
    u->type = UP_DOT;
    u->port = 853;
    char *at = strrchr(s, '@');
    if (at) { *at = 0; snprintf(u->hint, sizeof u->hint, "%s", at + 1); }
    char *colon = strrchr(s, ':');
    if (colon && strchr(colon, ':') == colon) { u->port = atoi(colon + 1); *colon = 0; }
    snprintf(u->host, sizeof u->host, "%s", s);
    if (!u->host[0]) return 0;
    snprintf(u->path, sizeof u->path, "%s", "");
    snprintf(u->text, sizeof u->text, "dot %s:%d", u->host, u->port);
    return 1;
}

/* 读文件到 tmp_up[]；返回 0 表示文件打不开（保留旧表） */
static int load_file(const char *path)
{
    FILE *f = fopen(path, "r");
    char line[320];
    int skipped = 0;
    if (!f) return 0;
    tmp_nup = 0;
    while (fgets(line, sizeof line, f)) {
        char *p = line;
        char w1[208], w2[208];
        int n1 = 0, n2 = 0;
        while (*p == ' ' || *p == '\t') p++;
        if (*p == '#' || *p == '\n' || *p == '\r' || *p == 0) continue;
        while (*p && *p != ' ' && *p != '\t' && *p != '\n' && *p != '\r' && n1 < (int)sizeof(w1) - 1)
            w1[n1++] = *p++;
        w1[n1] = 0;
        while (*p == ' ' || *p == '\t') p++;
        while (*p && *p != ' ' && *p != '\t' && *p != '\n' && *p != '\r' && n2 < (int)sizeof(w2) - 1)
            w2[n2++] = *p++;
        w2[n2] = 0;

        int ok = 0;
        if (strcmp(w1, "doh") == 0) {
#ifdef WITH_DOH
            ok = parse_doh(w2, &tmp_up[tmp_nup]);
#else
            if (g_verbose) fprintf(stderr, "dnsfwd: 本二进制未编译 DoH 支持，跳过 %s\n", w2);
#endif
        } else if (strcmp(w1, "dot") == 0) {
#ifdef WITH_DOH
            ok = parse_dot(w2, &tmp_up[tmp_nup]);
#else
            if (g_verbose) fprintf(stderr, "dnsfwd: 本二进制未编译 DoT 支持，跳过 %s\n", w2);
#endif
        } else {
            const char *ip = w1;
            if (strcmp(w1, "nameserver") == 0 || strcmp(w1, "server") == 0) {
                ip = w2;
                if (*ip == '=') ip++;
            } else if (strncmp(w1, "server=", 7) == 0) {
                ip = w1 + 7;
            }
            int r = (*ip) ? parse_upstream(ip, &tmp_up[tmp_nup]) : 0;
            if (r == 1) ok = 1;
            else if (r == -1) fprintf(stderr, "dnsfwd: 忽略回环上游 %s（会造成自我循环）\n", ip);
        }
        if (ok) {
            tmp_nup++;
            if (tmp_nup >= MAXUP) {
                fprintf(stderr, "dnsfwd: 上游数量超过上限 %d，后面的条目被忽略（请检查上游文件）\n", MAXUP);
                break;
            }
        } else if (w1[0]) {
            skipped++;
        }
    }
    fclose(f);
    if (skipped && g_verbose) fprintf(stderr, "dnsfwd: %s 中有 %d 行未能识别（已跳过）\n", path, skipped);
    return 1;
}

/* 重载：读文件 → 原子替换（拿锁） */
static int reload_upstreams(void)
{
    if (!load_file(g_conf)) return 0;
    pthread_mutex_lock(&up_lock);
    memcpy(up, tmp_up, sizeof(struct upstream) * tmp_nup);
    nup = tmp_nup;
    pthread_mutex_unlock(&up_lock);
    return 1;
}

/* 上游快照：转发/探测时拷出，避免长时间持锁 */
static int snapshot(struct upstream *dst)
{
    int n;
    pthread_mutex_lock(&up_lock);
    n = nup;
    memcpy(dst, up, sizeof(struct upstream) * n);
    pthread_mutex_unlock(&up_lock);
    return n;
}

/* ---------------- DNS 报文小工具 ---------------- */

static int build_query(const char *name, int qtype, unsigned char *q)
{
    int len = 0;
    q[len++] = 0x12; q[len++] = 0x34;              /* 固定 ID：单查询单连接，够用 */
    q[len++] = 0x01; q[len++] = 0x00;              /* RD */
    q[len++] = 0x00; q[len++] = 0x01;              /* QDCOUNT */
    for (int i = 0; i < 6; i++) q[len++] = 0;
    const char *p = name;
    while (*p) {
        const char *dot = strchr(p, '.');
        int l = dot ? (int)(dot - p) : (int)strlen(p);
        if (l <= 0 || len + l + 2 > 256) return -1;
        q[len++] = (unsigned char)l;
        memcpy(q + len, p, l); len += l;
        if (!dot) break;
        p = dot + 1;
    }
    q[len++] = 0;
    q[len++] = (unsigned char)(qtype >> 8); q[len++] = (unsigned char)(qtype & 0xff);
    q[len++] = 0x00; q[len++] = 0x01;              /* IN */
    return len;
}

static int is_fake_ip4(unsigned int be_addr)
{
    unsigned int a = ntohl(be_addr);
    return (a & 0xfffe0000u) == 0xc6120000u;       /* 198.18.0.0/15：mihomo 默认 fake-ip 段 */
}
static int is_fake_ip6(const unsigned char *p)
{
    return (p[0] & 0xfe) == 0xfc && (p[1] & 0xc0) == 0x00;   /* fc00::/18 近似判断 */
}

/* 解析应答，把 A/AAAA 追加到 out；返回记录数，*fake 置位表示命中 fake-ip 段 */
static int parse_answers(const unsigned char *r, int n, char *out, size_t outsz, int *fake)
{
    int cnt = 0;
    *fake = 0;
    out[0] = 0;
    if (n < 12) return 0;
    int ancount = (r[6] << 8) | r[7];
    int off = 12;
    /* 跳过 QNAME */
    while (off < n && r[off]) {
        if ((r[off] & 0xc0) == 0xc0) { off += 2; goto qdone; }
        off += r[off] + 1;
    }
    off++;
qdone:
    off += 4;
    for (int i = 0; i < ancount && off + 10 <= n; i++) {
        if ((r[off] & 0xc0) == 0xc0) off += 2;
        else { while (off < n && r[off]) off += r[off] + 1; off++; }
        if (off + 10 > n) break;
        int type = (r[off] << 8) | r[off + 1];
        int rdlen = (r[off + 8] << 8) | r[off + 9];
        off += 10;
        if (off + rdlen > n) break;
        char buf[INET6_ADDRSTRLEN];
        if (type == 1 && rdlen == 4) {
            struct in_addr a; memcpy(&a, r + off, 4);
            if (is_fake_ip4(a.s_addr)) *fake = 1;
            snprintf(buf, sizeof buf, "%s", inet_ntoa(a));
            if (cnt) strncat(out, ",", outsz - strlen(out) - 1);
            strncat(out, buf, outsz - strlen(out) - 1);
            cnt++;
        } else if (type == 28 && rdlen == 16) {
            struct in6_addr a; memcpy(&a, r + off, 16);
            if (is_fake_ip6(a.s6_addr)) *fake = 1;
            if (!inet_ntop(AF_INET6, &a, buf, sizeof buf)) buf[0] = 0;
            if (buf[0]) {
                if (cnt) strncat(out, ",", outsz - strlen(out) - 1);
                strncat(out, buf, outsz - strlen(out) - 1);
                cnt++;
            }
        }
        off += rdlen;
    }
    return cnt;
}

/* 一次 UDP 查询；成功返回应答长度，失败返回 -1；rtt 写入 *rtt_ms */
static int query_once(const struct upstream *u, const unsigned char *q, int qlen,
                      unsigned char *resp, int respsz, long *rtt_ms, int timeout_ms)
{
    int s = socket(u->family, SOCK_DGRAM, 0);
    if (s < 0) return -1;
    /* -i：把查询直接绑到物理网卡（绕过 TUN 的 fake-ip，拿真实 IP） */
    if (g_iface) setsockopt(s, SOL_SOCKET, SO_BINDTODEVICE, g_iface, strlen(g_iface));
    if (connect(s, (const struct sockaddr *)&u->sa, u->len) != 0) { close(s); return -1; }
    if (send(s, q, qlen, 0) != qlen) { close(s); return -1; }
    struct timespec t0, t1;
    clock_gettime(CLOCK_MONOTONIC, &t0);
    struct pollfd pfd; pfd.fd = s; pfd.events = POLLIN;
    int n = -1;
    if (poll(&pfd, 1, timeout_ms) > 0) {
        n = recv(s, resp, respsz, 0);
        if (n >= 12 && (resp[0] != q[0] || resp[1] != q[1])) n = -1;   /* 防串包 */
    }
    clock_gettime(CLOCK_MONOTONIC, &t1);
    close(s);
    if (rtt_ms) *rtt_ms = (t1.tv_sec - t0.tv_sec) * 1000 + (t1.tv_nsec - t0.tv_nsec) / 1000000;
    return n;
}

/* ---------------- DoH / DoT（加密上游；可选编译 WITH_DOH） ---------------- */
#ifdef WITH_DOH
static mbedtls_x509_crt g_ca;
static mbedtls_entropy_context g_ent;
static mbedtls_ctr_drbg_context g_drbg;
static int g_tls_ready = 0, g_ca_count = 0;
static const char *g_cadir[4];
static int g_ncadir = 0;
static pthread_mutex_t g_tlsmu = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_dcmu = PTHREAD_MUTEX_INITIALIZER;

/* 加载 CA：路径既可能是目录（系统 cacerts），也可能是单个 PEM 文件（模块打包的 doh-ca.pem）
 * ⚠️ 注意返回值语义：mbedTLS 3.x 的 mbedtls_x509_crt_parse* 返回的是**解析失败的张数**
 *    （0 表示全部成功），不是"加载了多少张"。所以这里以"证书链是否非空"为准。 */
static int ca_load(const char *path)
{
    struct stat st;
    int had = (g_ca.version != 0);
    if (stat(path, &st) != 0) return 0;
    int r;
    if (S_ISDIR(st.st_mode)) r = mbedtls_x509_crt_parse_path(&g_ca, path);
    else if (S_ISREG(st.st_mode)) r = mbedtls_x509_crt_parse_file(&g_ca, path);
    else return 0;
    if (r < 0) return r;                       /* 真正的错误（打不开/格式非法） */
    return (g_ca.version != 0 || had) ? 1 : 0; /* 只要链里有证书就算成功 */
}

/* 统计已加载的根证书张数（沿着链数） */
static int ca_count_certs(void)
{
    int n = 0;
    for (mbedtls_x509_crt *c = &g_ca; c != NULL; c = c->next)
        if (c->version != 0) n++;
    return n;
}

static void tls_init_once(void)
{
    if (g_tls_ready) return;
    pthread_mutex_lock(&g_tlsmu);
    if (!g_tls_ready) {
        mbedtls_x509_crt_init(&g_ca);
        mbedtls_entropy_init(&g_ent);
        mbedtls_ctr_drbg_init(&g_drbg);
        const char *pers = "dnsfwd-tls";
        if (mbedtls_ctr_drbg_seed(&g_drbg, mbedtls_entropy_func, &g_ent,
                                  (const unsigned char *)pers, strlen(pers)) == 0) {
            /* 优先用命令行动态指定的目录（模块的 pick_ca_dir 会传进来），
             * 否则依次尝试 Android 的常见位置（含 Android 14+ 的 apex）；
             * 也支持直接给一个 PEM 文件（模块会打包一份 doh-ca.pem，跨设备更可靠） */
            int loaded_any = 0;
            for (int i = 0; i < g_ncadir; i++) {
                int rc = ca_load(g_cadir[i]);
                if (rc < 0) fprintf(stderr, "dnsfwd: 加载 CA %s 失败（错误码 %d）\n", g_cadir[i], rc);
                else if (rc > 0) loaded_any = 1;
            }
            if (!loaded_any) {
                const char *defs[] = {
                    "/data/adb/modules/nine-router-go/system/etc/doh-ca.pem",
                    "/apex/com.android.conscrypt/cacerts",
                    "/system/etc/security/cacerts",
                    "/data/misc/keychain/cacerts-added",
                    "/etc/security/cacerts",
                    NULL
                };
                for (int i = 0; defs[i]; i++) {
                    int rc = ca_load(defs[i]);
                    if (rc < 0) fprintf(stderr, "dnsfwd: 加载 CA %s 失败（错误码 %d）\n", defs[i], rc);
                    else if (rc > 0) loaded_any = 1;
                }
            }
            g_ca_count = ca_count_certs();
        }
        g_tls_ready = 1;
        fprintf(stderr, "dnsfwd: TLS 就绪（系统 CA %d 张%s）\n", g_ca_count,
                g_ca_count > 0 ? "" : "；未找到 CA 目录，加密上游暂不校验证书");
    }
    pthread_mutex_unlock(&g_tlsmu);
}

static int tcp_connect(const struct upstream *u, int timeout_ms)
{
    int s = socket(u->family, SOCK_STREAM, 0);
    if (s < 0) return -1;
    struct timeval tv;
    tv.tv_sec = timeout_ms / 1000; tv.tv_usec = (timeout_ms % 1000) * 1000;
    setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
    setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
    if (g_iface) setsockopt(s, SOL_SOCKET, SO_BINDTODEVICE, g_iface, strlen(g_iface));
    if (connect(s, (const struct sockaddr *)&u->sa, u->len) != 0) { close(s); return -1; }
    return s;
}
static int bio_send(void *ctx, const unsigned char *buf, size_t len)
{
    int fd = (int)(long)ctx;
    ssize_t n = send(fd, buf, len, 0);
    return (n <= 0) ? MBEDTLS_ERR_NET_SEND_FAILED : (int)n;
}
static int bio_recv(void *ctx, unsigned char *buf, size_t len)
{
    int fd = (int)(long)ctx;
    ssize_t n = recv(fd, buf, len, 0);
    if (n < 0) return (errno == EAGAIN || errno == EWOULDBLOCK) ? MBEDTLS_ERR_SSL_WANT_READ : MBEDTLS_ERR_NET_RECV_FAILED;
    return (int)n;
}
static int ssl_write_all(mbedtls_ssl_context *ssl, const unsigned char *p, int n)
{
    int off = 0;
    while (off < n) {
        int w = mbedtls_ssl_write(ssl, p + off, n - off);
        if (w == MBEDTLS_ERR_SSL_WANT_READ || w == MBEDTLS_ERR_SSL_WANT_WRITE) continue;
        if (w <= 0) return -1;
        off += w;
    }
    return 0;
}

/* ============================================================
 * 加密上游连接池（DoH/DoT 复用）
 *   · 为什么需要：每次查询都重建 TCP+TLS 要 4 个 RTT（实测 400~500ms），
 *     复用之后同一上游的后续查询只需 1 个 RTT（约 30~100ms）——省电省流量也更快。
 *   · 键 = 上游展示串（host:port/path），每个槽位自带互斥锁：同一连接同时只被
 *     一个工作线程使用；被占用时新来的查询自己另开一条（不排队、不等待）。
 *   · 失败时**只重试一次**并重建连接（复用连接对端可能已关闭）。
 *   · 空闲超过 60 秒的连接在下次取用时关闭（避免占用 fd 与server 侧超时残连）。
 * ============================================================ */
#define POOL_SZ 6
struct tls_conn {
    int used;
    char key[256];
    int fd;
    mbedtls_ssl_context ssl;
    mbedtls_ssl_config conf;
    time_t last;
    pthread_mutex_t mu;
};
static struct tls_conn g_pool[POOL_SZ];
static int g_pool_ready = 0;
static pthread_mutex_t g_poolmu = PTHREAD_MUTEX_INITIALIZER;

static void conn_close(struct tls_conn *pc)
{
    if (pc->fd >= 0) {
        mbedtls_ssl_close_notify(&pc->ssl);
        mbedtls_ssl_free(&pc->ssl);
        mbedtls_ssl_config_free(&pc->conf);
        close(pc->fd);
        pc->fd = -1;
    }
}
/* 新建一条 TLS 连接（不做抢占，直接占用一个空槽） */
static struct tls_conn *pool_new_conn(const struct upstream *u, const char *key, int timeout_ms)
{
    tls_init_once();
    pthread_mutex_lock(&g_poolmu);
    if (!g_pool_ready) {
        for (int i = 0; i < POOL_SZ; i++) { g_pool[i].used = 0; g_pool[i].fd = -1; pthread_mutex_init(&g_pool[i].mu, NULL); }
        g_pool_ready = 1;
    }
    int slot = -1;
    for (int i = 0; i < POOL_SZ; i++) if (!g_pool[i].used) { slot = i; break; }
    if (slot < 0) { pthread_mutex_unlock(&g_poolmu); return NULL; }   /* 池满：本次不走加密 */
    g_pool[slot].used = 1;
    snprintf(g_pool[slot].key, sizeof g_pool[slot].key, "%s", key);
    g_pool[slot].fd = -1;
    pthread_mutex_lock(&g_pool[slot].mu);                              /* 立刻占用 */
    pthread_mutex_unlock(&g_poolmu);

    struct tls_conn *pc = &g_pool[slot];
    int fd = tcp_connect(u, timeout_ms);
    if (fd < 0) goto fail;
    mbedtls_ssl_init(&pc->ssl);
    mbedtls_ssl_config_init(&pc->conf);
    if (mbedtls_ssl_config_defaults(&pc->conf, MBEDTLS_SSL_IS_CLIENT, MBEDTLS_SSL_TRANSPORT_STREAM,
                                    MBEDTLS_SSL_PRESET_DEFAULT) != 0) { close(fd); goto fail; }
    mbedtls_ssl_conf_rng(&pc->conf, mbedtls_ctr_drbg_random, &g_drbg);
    mbedtls_ssl_conf_ca_chain(&pc->conf, &g_ca, NULL);
    mbedtls_ssl_conf_authmode(&pc->conf, g_ca_count > 0 ? MBEDTLS_SSL_VERIFY_REQUIRED : MBEDTLS_SSL_VERIFY_NONE);
    if (mbedtls_ssl_setup(&pc->ssl, &pc->conf) != 0) { mbedtls_ssl_config_free(&pc->conf); close(fd); goto fail; }
    mbedtls_ssl_set_hostname(&pc->ssl, u->host);
    mbedtls_ssl_set_bio(&pc->ssl, (void *)(long)fd, bio_send, bio_recv, NULL);
    int r;
    while ((r = mbedtls_ssl_handshake(&pc->ssl)) != 0) {
        if (r != MBEDTLS_ERR_SSL_WANT_READ && r != MBEDTLS_ERR_SSL_WANT_WRITE) {
            if (g_verbose) {
                char eb[128]; mbedtls_strerror(r, eb, sizeof eb);
                fprintf(stderr, "dnsfwd: TLS 握手失败（%s）：%s\n", u->host, eb);
            }
            mbedtls_ssl_free(&pc->ssl);
            mbedtls_ssl_config_free(&pc->conf);
            close(fd);
            goto fail;
        }
    }
    pc->fd = fd;
    pc->last = time(NULL);
    return pc;
fail:
    pc->fd = -1;
    pthread_mutex_lock(&g_poolmu);
    pc->used = 0;
    pthread_mutex_unlock(&g_poolmu);
    pthread_mutex_unlock(&pc->mu);
    return NULL;
}
/* 取一条可复用的连接（返回时已持有该槽位锁）；没有就返回 NULL */
static struct tls_conn *pool_get_conn(const char *key)
{
    struct tls_conn *pc = NULL;
    pthread_mutex_lock(&g_poolmu);
    for (int i = 0; i < POOL_SZ; i++) {
        if (!g_pool[i].used || strcmp(g_pool[i].key, key) != 0) continue;
        if (pthread_mutex_trylock(&g_pool[i].mu) == 0) { pc = &g_pool[i]; break; }
    }
    pthread_mutex_unlock(&g_poolmu);
    if (pc && pc->fd >= 0 && time(NULL) - pc->last > 60) {     /* 空闲过久：丢弃重建 */
        conn_close(pc);
        pc->fd = -1;
    }
    return pc;
}
/* 归还：keep=1 保留连接复用；keep=0 关闭并释放槽位 */
static void pool_put_conn(struct tls_conn *pc, int keep)
{
    if (!keep) {
        conn_close(pc);
        pthread_mutex_lock(&g_poolmu);
        pc->used = 0;
        pthread_mutex_unlock(&g_poolmu);
    } else {
        pc->last = time(NULL);
    }
    pthread_mutex_unlock(&pc->mu);
}

/* HTTP 头里取一个值（大小写不敏感，只匹配行首） */
static int hdr_val(const unsigned char *buf, int hdr_end, const char *name, char *out, int outsz)
{
    int nl = (int)strlen(name);
    for (int i = 0; i + nl + 1 < hdr_end; i++) {
        if (i > 0 && buf[i - 1] != '\n') continue;
        int j = 0;
        while (j < nl) {
            unsigned char a = buf[i + j], b = (unsigned char)name[j];
            if (a >= 'A' && a <= 'Z') a = (unsigned char)(a - 'A' + 'a');
            if (b >= 'A' && b <= 'Z') b = (unsigned char)(b - 'A' + 'a');
            if (a != b) break;
            j++;
        }
        if (j != nl || buf[i + nl] != ':') continue;
        int k = i + nl + 1;
        while (k < hdr_end && (buf[k] == ' ' || buf[k] == '\t')) k++;
        int m = 0;
        while (k < hdr_end && buf[k] != '\r' && buf[k] != '\n' && m < outsz - 1) out[m++] = (char)buf[k++];
        out[m] = 0;
        return 1;
    }
    return 0;
}
/* 解析 chunked 传输编码 */
static int dechunk(const unsigned char *p, int n, unsigned char *out, int outsz)
{
    int i = 0, o = 0;
    while (i < n) {
        int sz = 0, seen = 0;
        while (i < n && p[i] != '\r' && p[i] != '\n') {
            unsigned char c = p[i++];
            int d;
            if (c >= '0' && c <= '9') d = c - '0';
            else if (((c | 32) >= 'a') && ((c | 32) <= 'f')) d = (c | 32) - 'a' + 10;
            else break;
            sz = sz * 16 + d; seen = 1;
        }
        while (i < n && (p[i] == '\r' || p[i] == '\n')) i++;
        if (!seen) return -1;
        if (sz == 0) break;
        if (i + sz > n || o + sz > outsz) return -1;
        memcpy(out + o, p + i, sz);
        o += sz; i += sz;
        while (i < n && (p[i] == '\r' || p[i] == '\n')) i++;
    }
    return o;
}

/* DoH 一次请求（复用连接；Connection: keep-alive + 按 Content-Length 读） */
static int doh_exchange(struct tls_conn *pc, const struct upstream *u, const unsigned char *q, int qlen,
                        unsigned char *resp, int respsz)
{
    static __thread unsigned char buf[8192];
    char hdr[512];
    int hl = snprintf(hdr, sizeof hdr,
        "POST %s HTTP/1.1\r\nHost: %s\r\nAccept: application/dns-message\r\n"
        "Content-Type: application/dns-message\r\nContent-Length: %d\r\n\r\n",
        u->path[0] ? u->path : "/dns-query", u->host, qlen);
    if (ssl_write_all(&pc->ssl, (const unsigned char *)hdr, hl) != 0) return -1;
    if (ssl_write_all(&pc->ssl, q, qlen) != 0) return -1;

    int bl = 0, hdr_end = -1, cl = -1, chunked = 0;
    for (;;) {
        if (hdr_end < 0) {
            for (int i = 0; i + 3 < bl; i++)
                if (buf[i] == '\r' && buf[i+1] == '\n' && buf[i+2] == '\r' && buf[i+3] == '\n') { hdr_end = i + 4; break; }
        }
        if (hdr_end > 0) {
            if (cl < 0 && !chunked) {
                char v[64];
                if (hdr_val(buf, hdr_end, "Content-Length", v, sizeof v)) cl = atoi(v);
                else if (hdr_val(buf, hdr_end, "Transfer-Encoding", v, sizeof v) &&
                         (v[0] == 'c' || v[0] == 'C')) chunked = 1;
                else return -1;                       /* 无法定长 ⇒ 不能复用，判失败 */
            }
            if (cl >= 0 && bl >= hdr_end + cl) break;
            if (chunked && bl > hdr_end) {
                unsigned char tmp[BUFSZ];
                int n = dechunk(buf + hdr_end, bl - hdr_end, tmp, sizeof tmp);
                if (n > 0) {
                    if (n > respsz) return -1;
                    memcpy(resp, tmp, n);
                    return n;
                }
            }
        }
        if (bl >= (int)sizeof buf) return -1;
        int n = mbedtls_ssl_read(&pc->ssl, buf + bl, sizeof buf - bl);
        if (n == MBEDTLS_ERR_SSL_WANT_READ || n == MBEDTLS_ERR_SSL_WANT_WRITE) continue;
        if (n <= 0) return -1;
        bl += n;
    }
    if (bl < 20 || strncmp((char *)buf, "HTTP/1.", 7) != 0) return -1;
    char *code = strchr((char *)buf, ' ');
    if (!code || atoi(code + 1) != 200) return -1;
    if (cl <= 0 || cl > respsz || hdr_end + cl > bl) return -1;
    memcpy(resp, buf + hdr_end, cl);
    return cl;
}
/* DoH 查询：优先复用；失败则重建并只重试一次 */
static int doh_query(const struct upstream *u, const unsigned char *q, int qlen,
                     unsigned char *resp, int respsz, int timeout_ms)
{
    struct tls_conn *pc = pool_get_conn(u->text);
    int fresh = 0;
    if (!pc) { pc = pool_new_conn(u, u->text, timeout_ms); fresh = 1; }
    if (!pc) return -1;
    int n = (pc->fd >= 0) ? doh_exchange(pc, u, q, qlen, resp, respsz) : -1;
    if (n <= 0 && !fresh) {
        pool_put_conn(pc, 0);                          /* 复用失败：关掉，重建一次 */
        pc = pool_new_conn(u, u->text, timeout_ms);
        if (!pc) return -1;
        n = doh_exchange(pc, u, q, qlen, resp, respsz);
    }
    pool_put_conn(pc, n > 0);                          /* 成功 → 保留连接供复用 */
    return n;
}

/* DoT 一次请求（复用连接，2 字节长度前缀） */
static int dot_exchange(struct tls_conn *pc, const unsigned char *q, int qlen,
                        unsigned char *resp, int respsz)
{
    unsigned char framed[BUFSZ + 2];
    framed[0] = (unsigned char)(qlen >> 8);
    framed[1] = (unsigned char)(qlen & 0xff);
    memcpy(framed + 2, q, qlen);
    if (ssl_write_all(&pc->ssl, framed, qlen + 2) != 0) return -1;
    unsigned char hdr[2];
    int got = 0, n = 0;
    while (got < 2) {
        n = mbedtls_ssl_read(&pc->ssl, hdr + got, 2 - got);
        if (n == MBEDTLS_ERR_SSL_WANT_READ || n == MBEDTLS_ERR_SSL_WANT_WRITE) continue;
        if (n <= 0) return -1;
        got += n;
    }
    int want = (hdr[0] << 8) | hdr[1];
    if (want <= 0 || want > respsz) return -1;
    got = 0;
    while (got < want) {
        n = mbedtls_ssl_read(&pc->ssl, resp + got, want - got);
        if (n == MBEDTLS_ERR_SSL_WANT_READ || n == MBEDTLS_ERR_SSL_WANT_WRITE) continue;
        if (n <= 0) return -1;
        got += n;
    }
    return got;
}
static int dot_query(const struct upstream *u, const unsigned char *q, int qlen,
                     unsigned char *resp, int respsz, int timeout_ms)
{
    struct tls_conn *pc = pool_get_conn(u->text);
    int fresh = 0;
    if (!pc) { pc = pool_new_conn(u, u->text, timeout_ms); fresh = 1; }
    if (!pc) return -1;
    int n = (pc->fd >= 0) ? dot_exchange(pc, q, qlen, resp, respsz) : -1;
    if (n <= 0 && !fresh) {
        pool_put_conn(pc, 0);
        pc = pool_new_conn(u, u->text, timeout_ms);
        if (!pc) return -1;
        n = dot_exchange(pc, q, qlen, resp, respsz);
    }
    pool_put_conn(pc, n > 0);
    return n;
}
#endif /* WITH_DOH */

/* 自举解析缓存：DoH/DoT 主机名 → 地址（避免每个查询都去解析一遍） */
struct dns_cache { char host[128]; struct sockaddr_storage sa; socklen_t len; int family; };
static struct dns_cache g_dc[16];
static int g_ndc = 0;

/* ============================================================
 * 端到端锚点测量（-E host[:port]）
 *   为什么需要：DNS 解析快 ≠ 连模型快。解析器返回哪个 CDN/接入 IP，
 *   决定了你实际连到哪个节点；只看 DNS 延迟会选出"解析快但连接慢"的 DNS。
 *   做法：拿该解析器对这个域名的应答 IP，真的 TCP 连一次目标端口，量握手耗时。
 *   ⇒ 打分以"端到端可连性 + 握手耗时"为主，DNS 延迟只做次要参考。
 * ============================================================ */
#define MAX_ANCHOR 4
struct anchor { char host[128]; int port; };
static struct anchor g_anchor[MAX_ANCHOR];
static int g_nanchor = 0;

static int anchor_port_of(const char *host)
{
    for (int i = 0; i < g_nanchor; i++)
        if (strcmp(g_anchor[i].host, host) == 0) return g_anchor[i].port;
    return 0;
}

/* 对某个 IP 做一次 TCP 握手，返回毫秒；失败返回 -1 */
static long tcp_handshake_ms(int family, const struct sockaddr_storage *sa, socklen_t slen, int port, int timeout_ms)
{
    int s = socket(family, SOCK_STREAM, 0);
    if (s < 0) return -1;
    struct sockaddr_storage dst = *sa;
    if (family == AF_INET6) ((struct sockaddr_in6 *)&dst)->sin6_port = htons(port);
    else ((struct sockaddr_in *)&dst)->sin_port = htons(port);
    struct timeval tv;
    tv.tv_sec = timeout_ms / 1000; tv.tv_usec = (timeout_ms % 1000) * 1000;
    setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
    struct timespec t0, t1;
    clock_gettime(CLOCK_MONOTONIC, &t0);
    int r = connect(s, (const struct sockaddr *)&dst, slen);
    clock_gettime(CLOCK_MONOTONIC, &t1);
    close(s);
    if (r != 0) return -1;
    return (t1.tv_sec - t0.tv_sec) * 1000 + (t1.tv_nsec - t0.tv_nsec) / 1000000;
}

/* 取应答里的第一个 A/AAAA 到 sockaddr */
static int first_addr(const unsigned char *r, int n, struct sockaddr_storage *sa, socklen_t *slen, int *fam)
{
    if (n < 12) return 0;
    int an = (r[6] << 8) | r[7], off = 12;
    while (off < n && r[off]) {
        if ((r[off] & 0xc0) == 0xc0) { off += 2; goto qd; }
        off += r[off] + 1;
    }
    off++;
qd:
    off += 4;
    for (int i = 0; i < an && off + 12 <= n; i++) {
        if ((r[off] & 0xc0) == 0xc0) off += 2;
        else { while (off < n && r[off]) off += r[off] + 1; off++; }
        if (off + 10 > n) break;
        int type = (r[off] << 8) | r[off + 1];
        int rdlen = (r[off + 8] << 8) | r[off + 9];
        off += 10;
        if (type == 1 && rdlen == 4) {
            struct sockaddr_in *s = (struct sockaddr_in *)sa;
            memset(sa, 0, sizeof *sa);
            s->sin_family = AF_INET;
            memcpy(&s->sin_addr, r + off, 4);
            *slen = sizeof(*s); *fam = AF_INET; return 1;
        }
        if (type == 28 && rdlen == 16) {
            struct sockaddr_in6 *s = (struct sockaddr_in6 *)sa;
            memset(sa, 0, sizeof *sa);
            s->sin6_family = AF_INET6;
            memcpy(&s->sin6_addr, r + off, 16);
            *slen = sizeof(*s); *fam = AF_INET6; return 1;
        }
        off += rdlen;
    }
    return 0;
}

/* 用列表里的 UDP 上游把 host 解析成地址（自举；失败则用内置兜底） */
static int boot_resolve(const char *host, struct upstream *out)
{
    struct upstream list[MAXUP];
    int n = snapshot(list);
    unsigned char q[512], r[BUFSZ];
    int ql = build_query(host, 1, q);
    if (ql < 0) return 0;
    for (int i = 0; i <= n; i++) {
        struct upstream t;
        memset(&t, 0, sizeof t);
        if (i < n) {
            if (list[i].type != UP_UDP || list[i].len == 0) continue;
            t = list[i];
        } else if (!parse_upstream("223.5.5.5", &t)) {
            return 0;
        }
        long ms = 0;
        int rn = query_once(&t, q, ql, r, sizeof r, &ms, 1500);
        if (rn <= 0) continue;
        struct sockaddr_storage sa; socklen_t sl = 0; int fam = 0;
        if (!first_addr(r, rn, &sa, &sl, &fam)) continue;
        out->sa = sa; out->len = sl; out->family = fam;
        if (fam == AF_INET6) ((struct sockaddr_in6 *)&out->sa)->sin6_port = htons(out->port);
        else ((struct sockaddr_in *)&out->sa)->sin_port = htons(out->port);
        return 1;
    }
    return 0;
}

/* 确保 DoH/DoT 上游有可用地址：提示 IP > 缓存 > 自举解析 */
static int upstream_ready(struct upstream *u)
{
    if (u->type == UP_UDP) return u->len > 0;
    if (u->len > 0) return 1;
    /* 主机名本身就是 IP 字面量（如 doh https://223.5.5.5/dns-query）：
     * 直接用它，绝不能拿去当域名解析（否则必然失败） */
    {
        struct upstream h;
        if (parse_upstream(u->host, &h)) {
            u->sa = h.sa; u->len = h.len; u->family = h.family;
            if (u->family == AF_INET6) ((struct sockaddr_in6 *)&u->sa)->sin6_port = htons(u->port);
            else ((struct sockaddr_in *)&u->sa)->sin_port = htons(u->port);
            return 1;
        }
    }
    if (u->hint[0]) {
        struct upstream h;
        if (parse_upstream(u->hint, &h)) {
            u->sa = h.sa; u->len = h.len; u->family = h.family;
            if (u->family == AF_INET6) ((struct sockaddr_in6 *)&u->sa)->sin6_port = htons(u->port);
            else ((struct sockaddr_in *)&u->sa)->sin_port = htons(u->port);
            return 1;
        }
    }
    pthread_mutex_lock(&g_dcmu);
    for (int i = 0; i < g_ndc; i++) {
        if (strcmp(g_dc[i].host, u->host) == 0) {
            u->sa = g_dc[i].sa; u->len = g_dc[i].len; u->family = g_dc[i].family;
            if (u->family == AF_INET6) ((struct sockaddr_in6 *)&u->sa)->sin6_port = htons(u->port);
            else ((struct sockaddr_in *)&u->sa)->sin_port = htons(u->port);
            pthread_mutex_unlock(&g_dcmu);
            return 1;
        }
    }
    pthread_mutex_unlock(&g_dcmu);

    struct upstream t;
    memset(&t, 0, sizeof t);
    t.port = u->port;
    if (!boot_resolve(u->host, &t)) return 0;
    pthread_mutex_lock(&g_dcmu);
    if (g_ndc < (int)(sizeof g_dc / sizeof g_dc[0])) {
        snprintf(g_dc[g_ndc].host, sizeof g_dc[0].host, "%s", u->host);
        g_dc[g_ndc].sa = t.sa; g_dc[g_ndc].len = t.len; g_dc[g_ndc].family = t.family;
        g_ndc++;
    }
    pthread_mutex_unlock(&g_dcmu);
    u->sa = t.sa; u->len = t.len; u->family = t.family;
    return 1;
}

/* 统一入口：按上游类型分派（UDP / DoH / DoT），并统一测耗时 */
static int upstream_query(struct upstream *u, const unsigned char *q, int qlen,
                          unsigned char *resp, int respsz, long *rtt_ms, int timeout_ms)
{
    struct timespec t0, t1;
    if (!upstream_ready(u)) return -1;
    clock_gettime(CLOCK_MONOTONIC, &t0);
    int n;
    if (u->type == UP_DOH) {
#ifdef WITH_DOH
        n = doh_query(u, q, qlen, resp, respsz, timeout_ms);
#else
        n = -1;
#endif
    } else if (u->type == UP_DOT) {
#ifdef WITH_DOH
        n = dot_query(u, q, qlen, resp, respsz, timeout_ms);
#else
        n = -1;
#endif
    } else {
        n = query_once(u, q, qlen, resp, respsz, rtt_ms, timeout_ms);
    }
    clock_gettime(CLOCK_MONOTONIC, &t1);
    if (rtt_ms) *rtt_ms = (t1.tv_sec - t0.tv_sec) * 1000 + (t1.tv_nsec - t0.tv_nsec) / 1000000;
    return n;
}

/* 上游类型标签（探测输出用） */
/* 可回读的规范形式：用于**探测输出**与择优清单（9rctl 会把第一列原样写回上游文件）。
   为什么需要它：探测过去输出的是 text（`doh host:port/path`），
     · 丢了 `https://` ⇒ 写回后 parse_doh 认不出来；
     · 丢了 `@提示IP` ⇒ 需要自举解析的 DoH/DoT（doh.pub、dns.alidns.com…）在探测里
       自己解析不出主机名 ⇒ 全部超时 ⇒ 加密上游永远选不上（实测 9999ms/可用 0%）。
   为什么不直接改 text：text 同时是 **TLS 连接池的键**，改它会影响既有连接复用行为。
   ⇒ 只在这里派生一个"能原样回读"的字符串，text 保持不动。 */
static void up_ident(const struct upstream *u, char *dst, size_t n)
{
    if (u->type == UP_DOH)
        snprintf(dst, n, "doh https://%s:%d%s%s%s",
                 u->host, u->port, u->path, u->hint[0] ? "@" : "", u->hint);
    else if (u->type == UP_DOT)
        snprintf(dst, n, "dot %s:%d%s%s",
                 u->host, u->port, u->hint[0] ? "@" : "", u->hint);
    else
        snprintf(dst, n, "%s", u->text);   /* 明文：text 本身就是 IP，保持原样 */
}

static const char *up_kind(const struct upstream *u)
{
    if (u->type == UP_DOH) return "doh";
    if (u->type == UP_DOT) return "dot";
    return (u->family == AF_INET6) ? "v6" : "v4";
}

/* ============================================================
 * DNS 响应缓存（遵循 TTL）
 *   · 为什么需要：Go 的解析器自己不缓存，而我们绕过了 netd 的缓存 ⇒
 *     没有缓存时，同一个域名每次解析都要打到上游（费流量、费电、慢）。
 *   · 命中即回，不发任何网络包 ⇒ 这是"能效"最直接的来源。
 *   · 只缓存 RCODE=0 的成功应答；TTL 取所有记录的最小值（保守），并可设上限。
 *   · 键 = 问题段（去掉 12 字节头），大小写归一（DNS 不区分大小写）。
 *   · 命中时把应答里的 ID 改写成客户端查询的 ID（每个客户端 ID 都不同）。
 * ============================================================ */
#define CACHE_SZ        256
#define CACHE_RESP_MAX  1024        /* 超过这个大小的应答不缓存（罕见） */
#define CACHE_TTL_MAX   300         /* TTL 上限（秒），避免长期使用过期记录 */
#define CACHE_KEY_MAX   256

struct c_entry {
    int used;
    unsigned char key[CACHE_KEY_MAX];
    int keylen;
    unsigned char resp[CACHE_RESP_MAX];
    int resplen;
    time_t expire;
};
static struct c_entry g_cache[CACHE_SZ];
static pthread_mutex_t g_cachemu = PTHREAD_MUTEX_INITIALIZER;
static int g_cache_enable = 1;
static long g_cache_hits = 0, g_cache_miss = 0;

/* 生成缓存键：问题段（qname+qtype+qclass），字节小写化 */
static int cache_key(const unsigned char *q, int qlen, unsigned char *key)
{
    if (qlen <= 12 || qlen - 12 > CACHE_KEY_MAX) return 0;
    int n = qlen - 12;
    for (int i = 0; i < n; i++) {
        unsigned char c = q[12 + i];
        if (c >= 'A' && c <= 'Z') c = (unsigned char)(c - 'A' + 'a');
        key[i] = c;
    }
    return n;
}

/* 取应答里所有记录的最小 TTL；无法解析时返回 0（不缓存） */
static int min_ttl(const unsigned char *r, int n)
{
    if (n < 12) return 0;
    int qd = (r[4] << 8) | r[5];
    int an = (r[6] << 8) | r[7];
    int ns = (r[8] << 8) | r[9];
    int off = 12;
    for (int i = 0; i < qd; i++) {                     /* 跳过问题段 */
        while (off < n && r[off]) {
            if ((r[off] & 0xc0) == 0xc0) { off += 1; break; }
            off += r[off] + 1;
        }
        off += 1 + 4;
    }
    int best = -1;
    int total = an + ns;
    for (int i = 0; i < total && off + 10 <= n; i++) {
        if ((r[off] & 0xc0) == 0xc0) off += 2;
        else { while (off < n && r[off]) off += r[off] + 1; off++; }
        if (off + 10 > n) break;
        int ttl = (r[off + 4] << 24) | (r[off + 5] << 16) | (r[off + 6] << 8) | r[off + 7];
        int rdlen = (r[off + 8] << 8) | r[off + 9];
        if (ttl < 0) ttl = 0;
        if (best < 0 || ttl < best) best = ttl;
        off += 10 + rdlen;
    }
    return (best < 0) ? 0 : best;
}

static int cache_get(const unsigned char *q, int qlen, unsigned char *out, int outsz, int *outlen)
{
    if (!g_cache_enable) return 0;
    unsigned char key[CACHE_KEY_MAX];
    int klen = cache_key(q, qlen, key);
    if (klen <= 0) return 0;
    time_t now = time(NULL);
    int hit = 0;
    pthread_mutex_lock(&g_cachemu);
    for (int i = 0; i < CACHE_SZ; i++) {
        if (!g_cache[i].used || g_cache[i].keylen != klen) continue;
        if (memcmp(g_cache[i].key, key, klen) != 0) continue;
        if (now >= g_cache[i].expire) { g_cache[i].used = 0; continue; }
        if (g_cache[i].resplen <= outsz) {
            memcpy(out, g_cache[i].resp, g_cache[i].resplen);
            out[0] = q[0]; out[1] = q[1];              /* 改写为客户端查询 ID */
            *outlen = g_cache[i].resplen;
            hit = 1;
        }
        break;
    }
    if (hit) g_cache_hits++; else g_cache_miss++;
    pthread_mutex_unlock(&g_cachemu);
    return hit;
}

static void cache_put(const unsigned char *q, int qlen, const unsigned char *resp, int resplen)
{
    if (!g_cache_enable) return;
    if (resplen <= 12 || resplen > CACHE_RESP_MAX) return;
    if ((resp[3] & 0x0f) != 0) return;                 /* 只缓存成功应答 */
    if (resp[6] == 0 && resp[7] == 0) return;          /* 没有回答记录也不缓存 */
    unsigned char key[CACHE_KEY_MAX];
    int klen = cache_key(q, qlen, key);
    if (klen <= 0) return;
    int ttl = min_ttl(resp, resplen);
    if (ttl <= 0) return;
    if (ttl > CACHE_TTL_MAX) ttl = CACHE_TTL_MAX;
    time_t now = time(NULL);
    pthread_mutex_lock(&g_cachemu);
    int slot = -1;
    for (int i = 0; i < CACHE_SZ; i++) {               /* 先找空位/过期位/同键位 */
        if (!g_cache[i].used || now >= g_cache[i].expire) { slot = i; break; }
        if (g_cache[i].keylen == klen && memcmp(g_cache[i].key, key, klen) == 0) { slot = i; break; }
    }
    if (slot < 0) {                                    /* 全满：覆盖最早过期的那个 */
        time_t oldest = 0;
        for (int i = 0; i < CACHE_SZ; i++)
            if (slot < 0 || g_cache[i].expire < oldest) { oldest = g_cache[i].expire; slot = i; }
    }
    g_cache[slot].used = 1;
    memcpy(g_cache[slot].key, key, klen);
    g_cache[slot].keylen = klen;
    memcpy(g_cache[slot].resp, resp, resplen);
    g_cache[slot].resplen = resplen;
    g_cache[slot].expire = now + ttl;
    pthread_mutex_unlock(&g_cachemu);
}

/* ---------------- 自检（走本进程监听的端口） ---------------- */

static int selftest_addr(const char *name, const char *server, int family, int port)
{
    struct upstream u;
    memset(&u, 0, sizeof u);
    u.family = family;
    if (family == AF_INET) {
        struct sockaddr_in *s = (struct sockaddr_in *)&u.sa;
        s->sin_family = AF_INET; s->sin_port = htons(port);
        inet_pton(AF_INET, server, &s->sin_addr);
        u.len = sizeof(*s);
    } else {
        struct sockaddr_in6 *s = (struct sockaddr_in6 *)&u.sa;
        s->sin6_family = AF_INET6; s->sin6_port = htons(port);
        inet_pton(AF_INET6, server, &s->sin6_addr);
        u.len = sizeof(*s);
    }
    unsigned char q[512], r[BUFSZ];
    int qlen = build_query(name, 1, q);
    if (qlen < 0) { printf("FAILED badname\n"); return 2; }
    long ms = 0;
    int n = query_once(&u, q, qlen, r, sizeof r, &ms, 4000);
    if (n < 12) { printf("FAILED %s\n", n < 0 ? "timeout" : "short"); return 1; }
    int ancount = (r[6] << 8) | r[7], rcode = r[3] & 0x0f;
    char ans[256]; int fake = 0;
    parse_answers(r, n, ans, sizeof ans, &fake);
    printf("answer=%d rcode=%d %dms", ancount, rcode, (int)ms);
    if (ans[0]) printf(" ip=%s", ans);
    if (fake) printf(" [fake-ip]");
    printf("\n");
    return (rcode == 0 && ancount > 0) ? 0 : 1;
}

/* ---------------- 探测模式：并发测全部上游 ---------------- */

/* 聚合粒度 = 上游 × 域名：只有按域名分开，才能比较"同一域名在不同上游答案是否一致"
 * （这是识别劫持/污染的关键信号，见方案文档 §2.1） */
struct agg {
    int ok, total, fake;
    long rtts[64];
    int nrtt;
    char ans[8][INET6_ADDRSTRLEN + 1];   /* 去重后的答案，最多 8 个 */
    int nans;
    /* 端到端（该解析器的答案 IP 能否连上、握手多快）——锚点域名才有值 */
    int tcp_ok, tcp_total;
    long tcp_ms;
    char tcp_ip[INET6_ADDRSTRLEN + 1];
};
struct job { int uidx; int didx; int seq; };

static struct upstream p_up[MAXUP];
static int p_nup = 0;
static char p_domains[MAXDOMAINS][128];
static int p_ndomain = 0;
static int p_repeat = 2;
static int p_qtype = 1;
static struct agg p_agg[MAXUP * MAXDOMAINS];
#define AGG(u, d) (&p_agg[(u) * MAXDOMAINS + (d)])
static struct job p_jobs[MAXUP * MAXDOMAINS * 8];
static int p_njobs = 0, p_nextjob = 0;
static pthread_mutex_t p_mu = PTHREAD_MUTEX_INITIALIZER;

static int cmp_long(const void *a, const void *b)
{
    long x = *(const long *)a, y = *(const long *)b;
    return (x > y) - (x < y);
}

/* 探测时限制 DoH/DoT 并发：TLS 握手要经过全局锁与连接池，
   8 个 worker 一起挤会让多数握手直接失败（实测 j=1 / j=4 基本全通，j=8 挂掉 4 条）。
   这里只限探测路径，**不影响转发服务**。 */
/* 实测结论（本机网络，命中同一批端点）：
     j=1 全通（doh 306 / doh.pub 787 / dot 246 / dot.pub 1048ms）
     j=2~4 开始出现挂（限流到 2 仍有 3/6 失败）
     j=8 大面积挂
   ⇒ 加密上游的握手对并发极度敏感，取 1（完全串行）换取可靠。
   代价有限：加密候选只有几条，串行多花几秒；探测是后台低频任务。 */
#define TLS_PROBE_MAX_CONC 1
static pthread_mutex_t p_tlsmu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t  p_tlscv = PTHREAD_COND_INITIALIZER;
static int p_tlsbusy = 0;
static void tls_probe_acquire(void)
{
    pthread_mutex_lock(&p_tlsmu);
    while (p_tlsbusy >= TLS_PROBE_MAX_CONC) pthread_cond_wait(&p_tlscv, &p_tlsmu);
    p_tlsbusy++;
    pthread_mutex_unlock(&p_tlsmu);
}
static void tls_probe_release(void)
{
    pthread_mutex_lock(&p_tlsmu);
    if (p_tlsbusy > 0) p_tlsbusy--;
    pthread_cond_signal(&p_tlscv);
    pthread_mutex_unlock(&p_tlsmu);
}

static void *probe_worker(void *arg)
{
    (void)arg;
    unsigned char q[512], r[BUFSZ];
    for (;;) {
        pthread_mutex_lock(&p_mu);
        if (p_nextjob >= p_njobs) { pthread_mutex_unlock(&p_mu); return NULL; }
        struct job j = p_jobs[p_nextjob++];
        pthread_mutex_unlock(&p_mu);

        int uidx = j.uidx;
        int qlen = build_query(p_domains[j.didx], p_qtype, q);
        long ms = 0;
        int n;
        if (p_up[uidx].type == UP_UDP) {
            n = (qlen > 0) ? upstream_query(&p_up[uidx], q, qlen, r, sizeof r, &ms, PROBE_TIMEOUT_MS) : -1;
        } else {
            tls_probe_acquire();
            n = (qlen > 0) ? upstream_query(&p_up[uidx], q, qlen, r, sizeof r, &ms, PROBE_TIMEOUT_MS_TLS) : -1;
            tls_probe_release();
        }
        char ans[256]; ans[0] = 0;
        int fake = 0, cnt = 0;
        if (n > 0) cnt = parse_answers(r, n, ans, sizeof ans, &fake);

        /* 锚点域名：拿这个解析器给的 IP，真的连一次目标端口（端到端质量） */
        long tcpms = -1;
        char tcpip[INET6_ADDRSTRLEN + 1];
        tcpip[0] = 0;
        int aport = anchor_port_of(p_domains[j.didx]);
        if (aport > 0 && n > 0) {
            struct sockaddr_storage sa; socklen_t sl = 0; int fam = 0;
            if (first_addr(r, n, &sa, &sl, &fam)) {
                if (fam == AF_INET6) inet_ntop(AF_INET6, &((struct sockaddr_in6 *)&sa)->sin6_addr, tcpip, sizeof tcpip);
                else inet_ntop(AF_INET, &((struct sockaddr_in *)&sa)->sin_addr, tcpip, sizeof tcpip);
                tcpms = tcp_handshake_ms(fam, &sa, sl, aport, PROBE_TCP_TIMEOUT_MS);
            }
        }

        pthread_mutex_lock(&p_mu);
        struct agg *a = AGG(uidx, j.didx);
        a->total++;
        if (aport > 0) {
            a->tcp_total++;
            if (tcpms >= 0) { a->tcp_ok++; a->tcp_ms = tcpms; if (tcpip[0]) snprintf(a->tcp_ip, sizeof a->tcp_ip, "%s", tcpip); }
            else if (tcpip[0]) snprintf(a->tcp_ip, sizeof a->tcp_ip, "%s", tcpip);
        }
        if (n > 0 && cnt > 0 && (r[3] & 0x0f) == 0) {
            a->ok++;
            if (a->nrtt < (int)(sizeof a->rtts / sizeof a->rtts[0])) a->rtts[a->nrtt++] = ms;
            if (fake) a->fake++;
            /* 答案去重收集（同一域名多次探测、同上游多答案都收敛成一份） */
            char *sp = ans;
            while (sp && *sp) {
                char *comma = strchr(sp, ',');
                if (comma) *comma = 0;
                if (*sp) {
                    int dup = 0;
                    for (int k = 0; k < a->nans; k++) if (strcmp(a->ans[k], sp) == 0) { dup = 1; break; }
                    if (!dup && a->nans < (int)(sizeof a->ans / sizeof a->ans[0]))
                        snprintf(a->ans[a->nans++], sizeof a->ans[0], "%s", sp);
                }
                sp = comma ? comma + 1 : NULL;
            }
        }
        pthread_mutex_unlock(&p_mu);
    }
    return NULL;
}

/* 返回 0 表示成功（结果已打印） */
static int probe_all(int workers, int tsv)
{
    if (!load_file(g_conf)) { fprintf(stderr, "dnsfwd: 无法读取 %s\n", g_conf); return 1; }
    p_nup = tmp_nup;
    if (p_nup == 0) { fprintf(stderr, "dnsfwd: %s 里没有可用上游\n", g_conf); return 1; }
    /* 原子替换到运行表（探测模式下也保持一致） */
    pthread_mutex_lock(&up_lock);
    memcpy(up, tmp_up, sizeof(struct upstream) * p_nup); nup = p_nup;
    pthread_mutex_unlock(&up_lock);
    snapshot(p_up);

    /* 探测前置：先把 DoH/DoT 的地址解析出来，并**把结果写回 hint**。
       为什么：boot_resolve() 本来就会自举解析（用明文上游，兜底 223.5.5.5），
       但解析结果过去只留在内存里 ⇒ 探测输出与随之写回的清单都**不带 @提示IP** ⇒
       每次都要重解析，失败时还是静默的。这里把"自举解析"落实到**持久化**：
         · 解析成功且 host 不是 IP 字面量 ⇒ 把 IP 写进 hint（之后 up_ident() 会带出来）；
         · 解析失败 ⇒ 明确告警（不再静默失败）。
       全部在单线程、快照 p_up[] 上做 ⇒ 不碰转发表 up[]，也不干扰并发。 */
    for (int u = 0; u < p_nup; u++) {
        struct upstream *pu = &p_up[u];
        if (pu->type == UP_UDP) continue;
        if (!upstream_ready(pu)) {
            fprintf(stderr, "dnsfwd: 无法确定 %s 的地址（探测中不可用；建议在候选池写 @提示IP）\n",
                    pu->host[0] ? pu->host : pu->text);
            continue;
        }
        if (!pu->hint[0] && pu->host[0]) {
            struct upstream probe_ip;
            if (!parse_upstream(pu->host, &probe_ip)) {   /* host 不是 IP 字面量才需要写回 */
                char ip[INET6_ADDRSTRLEN + 1];
                ip[0] = 0;
                if (pu->family == AF_INET6)
                    inet_ntop(AF_INET6, &((struct sockaddr_in6 *)&pu->sa)->sin6_addr, ip, sizeof ip);
                else if (pu->family == AF_INET)
                    inet_ntop(AF_INET, &((struct sockaddr_in *)&pu->sa)->sin_addr, ip, sizeof ip);
                if (ip[0]) snprintf(pu->hint, sizeof pu->hint, "%s", ip);
            }
        }
    }

    if (p_ndomain == 0) { snprintf(p_domains[0], sizeof p_domains[0], "www.baidu.com"); p_ndomain = 1; }

    p_njobs = 0;
    for (int u = 0; u < p_nup; u++)
        for (int d = 0; d < p_ndomain; d++)
            for (int s = 0; s < p_repeat; s++) {
                if (p_njobs >= (int)(sizeof p_jobs / sizeof p_jobs[0])) break;
                p_jobs[p_njobs].uidx = u; p_jobs[p_njobs].didx = d; p_jobs[p_njobs].seq = s;
                p_njobs++;
            }
    memset(p_agg, 0, sizeof p_agg);

    if (workers < 1) workers = 4;
    if (workers > 16) workers = 16;
    pthread_t th[16];
    int nth = workers;
    for (int i = 0; i < nth; i++) pthread_create(&th[i], NULL, probe_worker, NULL);
    for (int i = 0; i < nth; i++) pthread_join(th[i], NULL);

    if (tsv) printf("upstream\tfamily\tdomain\trtt_ms\tok/total\tn_ans\tanswers\tfakeip\ttcp_ms\ttcp_ok/total\ttcp_ip\n");
    for (int u = 0; u < p_nup; u++) {
        const char *fam = up_kind(&p_up[u]);
        char ident[300];                 /* 可回读的规范形式（含 @提示IP），供 9rctl 写回上游文件 */
        up_ident(&p_up[u], ident, sizeof ident);
        for (int d = 0; d < p_ndomain; d++) {
            struct agg *a = AGG(u, d);
            long p50 = -1;
            if (a->nrtt > 0) {
                qsort(a->rtts, a->nrtt, sizeof(long), cmp_long);
                p50 = a->rtts[a->nrtt / 2];
            }
            char j3[256];
            j3[0] = 0;
            for (int k = 0; k < a->nans && k < 3; k++) {
                if (j3[0]) strncat(j3, ",", sizeof j3 - strlen(j3) - 1);
                strncat(j3, a->ans[k], sizeof j3 - strlen(j3) - 1);
            }
            if (tsv) {
                printf("%s\t%s\t%s\t%ld\t%d/%d\t%d\t%s\t%s\t%ld\t%d/%d\t%s\n",
                       ident, fam, p_domains[d], p50, a->ok, a->total, a->nans,
                       j3[0] ? j3 : "-", a->fake > 0 ? "yes" : "no",
                       a->tcp_total > 0 ? a->tcp_ms : -1, a->tcp_ok, a->tcp_total,
                       a->tcp_ip[0] ? a->tcp_ip : "-");
            } else {
                printf("  %-34s %-3s %-24s %6ldms  %d/%d  %s%s",
                       ident, fam, p_domains[d], p50, a->ok, a->total,
                       j3[0] ? j3 : "-", a->fake > 0 ? "  ← fake-ip(TUN 接管)" : "");
                if (a->tcp_total > 0)
                    printf("  | 端到端 %ldms %s", a->tcp_ms, a->tcp_ip[0] ? a->tcp_ip : "");
                printf("\n");
            }
        }
    }
    return 0;
}

/* ============================================================
 * 转发工作池：并发 + 健康优先 + 快速失败
 *
 * 旧实现的问题（实测）：
 *   1) 一个上游试完再试下一个 ⇒ 最坏 ≈ 上游数 × 3s（5 个上游就是 15s），
 *      期间其它客户端的查询全被阻塞（Go 会并发发起多个查询，表现尤其差）；
 *   2) 坏上游永远排在前面（列表顺序固定）⇒ 每次都要先浪费一个 3s 超时；
 *   3) 全部失败时"沉默"⇒ 客户端只能等自己的超时，而不是立刻改用下一个 nameserver。
 *
 * 现在的做法：
 *   · 工作池（默认 4 线程）并发处理客户端查询，互不阻塞；
 *   · 每个上游维护「连续失败数 + 平滑延迟」，按健康度排序 ⇒ 好多上游自然排前面、坏的上游沉底；
 *   · 单次尝试用短超时（首个 800ms，之后按剩余预算均分，总预算仍是 3s）；
 *   · 全部失败立即回 SERVFAIL ⇒ Go 解析器马上回落到 resolv.conf 的下一个 nameserver。
 * ============================================================ */
#define FWD_WORKERS_DEF  4
#define JOBSZ            64
#define ATTEMPT_FIRST_MS 800
#define ATTEMPT_MIN_MS   300
/* DoH/DoT 首次要建立 TCP + TLS（约 4 个 RTT），预算要给得比明文宽，
 * 否则冷连接会被判超时、白白回落到明文（实测：DoH 首次 250~400ms，冷启动更慢） */
#define ATTEMPT_TLS_FIRST_MS 2000

struct up_stat { long rtt_ewma; int fails; };
static struct up_stat g_st[MAXUP];
static int g_workers = FWD_WORKERS_DEF;

struct qjob {
    int fd;
    int qlen;
    unsigned char q[BUFSZ];
    struct sockaddr_storage cli;
    socklen_t clilen;
};
static struct qjob g_q[JOBSZ];
static int g_qh = 0, g_qt = 0, g_qn = 0;
static pthread_mutex_t g_qmu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t g_qcv = PTHREAD_COND_INITIALIZER;
static pthread_t g_wth[16];

/* 构造 SERVFAIL 应答：把客户端请求原样回一张"服务器失败" */
static int build_servfail(const unsigned char *q, int qlen, unsigned char *out)
{
    if (qlen < 12) return 0;
    memcpy(out, q, qlen);
    out[2] = 0x81;                                     /* QR=1 */
    out[3] = (unsigned char)((q[3] & 0x01) | 0x02);    /* 保留 RD，RCODE=2 */
    out[6] = 0; out[7] = 0;                            /* ANCOUNT */
    out[8] = 0; out[9] = 0;                            /* NSCOUNT */
    out[10] = 0; out[11] = 0;                          /* ARCOUNT */
    return qlen;
}

/* 按健康度排序（插入排序，n ≤ 32）：失败少的在前，其次延迟小的在前 */
static void order_by_health(int *idx, int n)
{
    for (int i = 0; i < n; i++) idx[i] = i;
    for (int i = 1; i < n; i++) {
        int k = idx[i], j = i - 1;
        while (j >= 0) {
            int a = idx[j];
            int better = (g_st[k].fails != g_st[a].fails)
                       ? (g_st[k].fails < g_st[a].fails)
                       : (g_st[k].rtt_ewma < g_st[a].rtt_ewma);
            if (!better) break;
            idx[j + 1] = idx[j]; j--;
        }
        idx[j + 1] = k;
    }
}

static void stat_ok(int u, long ms)
{
    if (ms <= 0) ms = 1;
    if (g_st[u].rtt_ewma <= 0) g_st[u].rtt_ewma = ms;
    else g_st[u].rtt_ewma = (g_st[u].rtt_ewma * 3 + ms) / 4;      /* EWMA */
    g_st[u].fails = 0;
}
static void stat_fail(int u) { if (g_st[u].fails < 1000) g_st[u].fails++; }

/* 处理一个客户端查询 */
static void handle_query(const struct qjob *jb)
{
    struct upstream snap[MAXUP];
    int cnt = snapshot(snap);
    if (cnt <= 0) return;

    int idx[MAXUP];
    order_by_health(idx, cnt);

    unsigned char r[BUFSZ];
    /* 1) 命中缓存 → 直接回，零网络开销（省电、快；TTL 由上游决定） */
    {
        int clen = 0;
        if (cache_get(jb->q, jb->qlen, r, sizeof r, &clen)) {
            sendto(jb->fd, r, clen, 0, (const struct sockaddr *)&jb->cli, jb->clilen);
            if (g_verbose) fprintf(stderr, "dnsfwd: 缓存命中（命中 %ld / 未命中 %ld）\n", g_cache_hits, g_cache_miss);
            return;
        }
    }

    long budget = TIMEOUT_MS;
    for (int i = 0; i < cnt; i++) {
        /* 首个尝试的预算按上游类型区分：明文 800ms，加密 2000ms（要建 TLS） */
        long first = (snap[idx[i]].type == UP_UDP) ? ATTEMPT_FIRST_MS : ATTEMPT_TLS_FIRST_MS;
        long per = (i == 0) ? first : (budget > ATTEMPT_MIN_MS ? budget : ATTEMPT_MIN_MS);
        if (per > budget && budget > 0) per = budget;
        long ms = 0;
        int rn = upstream_query(&snap[idx[i]], jb->q, jb->qlen, r, sizeof r, &ms, (int)per);
        budget -= ms;
        if (rn > 0) {
            cache_put(jb->q, jb->qlen, r, rn);          /* 成功应答按 TTL 缓存 */
            if (sendto(jb->fd, r, rn, 0, (const struct sockaddr *)&jb->cli, jb->clilen) >= 0)
                stat_ok(idx[i], ms);
            return;
        }
        stat_fail(idx[i]);
        if (budget <= 0) break;
    }
    /* 全部失败：立即回 SERVFAIL（不沉默），让 Go 马上换下一个 nameserver */
    unsigned char sf[BUFSZ];
    int n = build_servfail(jb->q, jb->qlen, sf);
    if (n > 0) sendto(jb->fd, sf, n, 0, (const struct sockaddr *)&jb->cli, jb->clilen);
    fprintf(stderr, "dnsfwd: 全部上游不可用（%d 个），已回 SERVFAIL\n", cnt);
}

static void *fwd_worker(void *arg)
{
    (void)arg;
    for (;;) {
        pthread_mutex_lock(&g_qmu);
        while (g_qn == 0 && !g_stop) pthread_cond_wait(&g_qcv, &g_qmu);
        if (g_stop && g_qn == 0) { pthread_mutex_unlock(&g_qmu); return NULL; }
        struct qjob jb = g_q[g_qh];
        g_qh = (g_qh + 1) % JOBSZ; g_qn--;
        pthread_mutex_unlock(&g_qmu);
        handle_query(&jb);
    }
}

/* ---------------- 主程序 ---------------- */

static void usage(void)
{
    printf("用法: dnsfwd [选项]\n"
           "  -f <文件>     上游列表（默认 %s）\n"
           "  -b loopback   只绑 127.0.0.1 与 ::1（默认，安全）\n"
           "  -b any        绑 [::]:53 双栈全网卡（局域网共享；仅限可信网络）\n"
           "  -p <端口>     监听端口（默认 53；调试可用 5354）\n"
           "  -i <网卡>     上游 socket 绑到该网卡（SO_BINDTODEVICE，绕过 TUN）\n"
           "  -A <证书目录> 指定系统 CA 目录（DoH/DoT 校验用；可重复）\n"
           "  -c <秒>       响应缓存 TTL 上限（默认 %d；0 = 关闭缓存）\n"
           "  -E <锚点>     端到端测量：host[:port][,host[:port]]…（用你的上游 API 域名，\n"
           "                会拿解析出来的 IP 真连一次该端口，量握手耗时）\n"
           "  -t <域名>     自检：走本进程监听端口解析一次\n"
           "  -P            探测模式：并发测全部上游（配合 -d/-n）\n"
           "  -d <域名>     探测用域名（可重复，最多 %d 个）\n"
           "  -n <次数>     每个上游每域名的探测次数（默认 2）\n"
           "  -j <并发数>   探测并发（默认 6，最大 16）\n"
           "  -w <并发数>   转发工作线程数（默认 4，最大 16）\n"
           "  -T a|aaaa     查询类型（默认 a）\n"
           "  --tsv         探测结果输出 TSV（给脚本用）\n"
           "  -v            详细日志\n", g_conf, MAXDOMAINS, CACHE_TTL_MAX);
}

int main(int argc, char **argv)
{
    const char *test = NULL, *bindmode = "loopback";
    int port = 53, probe = 0, workers = 6, tsv = 0;

    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "-f") && i + 1 < argc) g_conf = argv[++i];
        else if (!strcmp(argv[i], "-t") && i + 1 < argc) test = argv[++i];
        else if (!strcmp(argv[i], "-p") && i + 1 < argc) port = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-b") && i + 1 < argc) bindmode = argv[++i];
        else if (!strcmp(argv[i], "-i") && i + 1 < argc) g_iface = argv[++i];
        else if (!strcmp(argv[i], "-P")) probe = 1;
        else if (!strcmp(argv[i], "-d") && i + 1 < argc) {
            if (p_ndomain < MAXDOMAINS) snprintf(p_domains[p_ndomain++], 128, "%s", argv[++i]);
            else i++;
        }
        else if (!strcmp(argv[i], "-n") && i + 1 < argc) p_repeat = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-j") && i + 1 < argc) workers = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-w") && i + 1 < argc) g_workers = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-c") && i + 1 < argc) {   /* 缓存 TTL 上限（秒）；0 = 关闭缓存 */
            int v = atoi(argv[++i]);
            g_cache_enable = (v > 0) ? 1 : 0;
        }
        else if (!strcmp(argv[i], "-E") && i + 1 < argc) {   /* 端到端锚点：host[:port][,host[:port]]… */
            char *spec = argv[++i];
            char *sp = spec;
            while (sp && *sp && g_nanchor < MAX_ANCHOR) {
                char *comma = strchr(sp, ',');
                if (comma) *comma = 0;
                char *colon = strrchr(sp, ':');
                int aport = 443;
                if (colon && strchr(colon + 1, ':') == NULL && atoi(colon + 1) > 0) {
                    aport = atoi(colon + 1);
                    *colon = 0;
                }
                if (*sp) {
                    snprintf(g_anchor[g_nanchor].host, sizeof g_anchor[0].host, "%s", sp);
                    g_anchor[g_nanchor].port = aport;
                    g_nanchor++;
                    if (p_ndomain < MAXDOMAINS) snprintf(p_domains[p_ndomain++], 128, "%s", sp);
                }
                sp = comma ? comma + 1 : NULL;
            }
        }
        else if (!strcmp(argv[i], "-A") && i + 1 < argc) {
            if (g_ncadir < (int)(sizeof g_cadir / sizeof g_cadir[0])) g_cadir[g_ncadir++] = argv[++i];
            else i++;
        }
        else if (!strcmp(argv[i], "-T") && i + 1 < argc) {
            const char *t = argv[++i];
            p_qtype = (t[0] == 'a' || t[0] == 'A') && (t[1] == 'a' || t[1] == 'A') ? 28 : 1;
        }
        else if (!strcmp(argv[i], "--tsv")) tsv = 1;
        else if (!strcmp(argv[i], "-v")) g_verbose = 1;
        else if (!strcmp(argv[i], "-h") || !strcmp(argv[i], "--help")) { usage(); return 0; }
    }
    if (port <= 0 || port > 65535) port = 53;
    if (p_repeat < 1) p_repeat = 1;
    if (p_repeat > 8) p_repeat = 8;

    if (probe) {
        signal(SIGPIPE, SIG_IGN);
        return probe_all(workers, tsv);
    }

    if (!load_file(g_conf)) {
        /* 文件不存在时不算致命：仍可 -t 自检或按旧行为报错 */
        if (!test) { fprintf(stderr, "dnsfwd: 无法读取上游文件 %s\n", g_conf); return 1; }
    } else {
        pthread_mutex_lock(&up_lock);
        memcpy(up, tmp_up, sizeof(struct upstream) * tmp_nup); nup = tmp_nup;
        pthread_mutex_unlock(&up_lock);
    }

    if (test) {
        /* 两条回落路径都要验：Go 会先试 [::1] 再试 127.0.0.1 */
        int rc4, rc6;
        printf("127.0.0.1:%d ", port);
        rc4 = selftest_addr(test, "127.0.0.1", AF_INET, port);
        printf("[::1]:%d     ", port);
        rc6 = selftest_addr(test, "::1", AF_INET6, port);
        return (rc4 == 0 && rc6 == 0) ? 0 : 1;
    }
    if (nup == 0) { fprintf(stderr, "dnsfwd: %s 里没有可用的 nameserver\n", g_conf); return 1; }

    signal(SIGHUP, on_hup);
    signal(SIGTERM, on_term);
    signal(SIGINT, on_term);
    signal(SIGPIPE, SIG_IGN);

    int fds[2], nfd = 0;
    if (!strcmp(bindmode, "any")) {
        int fd = socket(AF_INET6, SOCK_DGRAM, 0);
        if (fd < 0) { perror("socket6"); return 1; }
        int on = 0;                                     /* 双栈：v4 也收（局域网共享用） */
        setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &on, sizeof on);
        int one = 1; setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
        struct sockaddr_in6 a; memset(&a, 0, sizeof a);
        a.sin6_family = AF_INET6; a.sin6_port = htons(port); a.sin6_addr = in6addr_any;
        if (bind(fd, (struct sockaddr *)&a, sizeof a) != 0) {
            if (errno == EADDRINUSE)
                fprintf(stderr, "dnsfwd: 端口 %d 已被占用。用 `netstat -tulnp | grep :%d` 查看持有者\n", port, port);
            else perror("bind [::]");
            return 1;
        }
        fds[nfd++] = fd;
    } else {
        int f4 = socket(AF_INET, SOCK_DGRAM, 0);
        int one = 1; setsockopt(f4, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
        struct sockaddr_in a4; memset(&a4, 0, sizeof a4);
        a4.sin_family = AF_INET; a4.sin_port = htons(port); a4.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        if (bind(f4, (struct sockaddr *)&a4, sizeof a4) != 0) {
            if (errno == EADDRINUSE)
                fprintf(stderr, "dnsfwd: 127.0.0.1:%d 已被占用（可能已有实例在跑）。先 `9rctl dns-fwd stop`\n", port);
            else perror("bind 127.0.0.1");
            return 1;
        }
        fds[nfd++] = f4;
        int f6 = socket(AF_INET6, SOCK_DGRAM, 0);
        if (f6 >= 0) {
            int only = 1; setsockopt(f6, IPPROTO_IPV6, IPV6_V6ONLY, &only, sizeof only);
            setsockopt(f6, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
            struct sockaddr_in6 a6; memset(&a6, 0, sizeof a6);
            a6.sin6_family = AF_INET6; a6.sin6_port = htons(port); a6.sin6_addr = in6addr_loopback;
            if (bind(f6, (struct sockaddr *)&a6, sizeof a6) == 0) fds[nfd++] = f6;
            else { close(f6); fprintf(stderr, "dnsfwd: 警告：[::1]:%d 绑定失败，仅 IPv4 回环可用\n", port); }
        }
    }

    fprintf(stderr, "dnsfwd: 监听 %s:%d（%s），上游 %d 个，来自 %s%s\n",
            !strcmp(bindmode, "any") ? "0.0.0.0/::" : "127.0.0.1+::1", port,
            !strcmp(bindmode, "any") ? "全网卡·局域网可见" : "仅本机回环", nup, g_conf,
            g_iface ? "（上游绑定网卡，绕过 TUN）" : "");
    fprintf(stderr, "dnsfwd: 工作线程 %d，缓存 %s（TTL 上限 %ds，槽位 %d）\n",
            g_workers, g_cache_enable ? "开" : "关", CACHE_TTL_MAX, CACHE_SZ);
    if (g_verbose) {
        for (int i = 0; i < nup; i++) fprintf(stderr, "  upstream[%d] %s\n", i, up[i].text);
    }
    /* 启动转发工作池（并发处理客户端查询，互不阻塞） */
    if (g_workers < 1) g_workers = 1;
    if (g_workers > 16) g_workers = 16;
    for (int i = 0; i < g_workers; i++) {
        if (pthread_create(&g_wth[i], NULL, fwd_worker, NULL) != 0) { g_workers = i; break; }
    }
    if (g_workers <= 0) { fprintf(stderr, "dnsfwd: 工作线程创建失败，退化为单线程\n"); }

    /* 收包缓冲随任务传递，无需此处局部变量 */
    struct pollfd pfds[2];
    struct stat st; time_t last_mtime = 0, last_check = 0;
    if (stat(g_conf, &st) == 0) last_mtime = st.st_mtime;

    for (;;) {
        if (g_stop) break;
        for (int i = 0; i < nfd; i++) { pfds[i].fd = fds[i]; pfds[i].events = POLLIN; pfds[i].revents = 0; }
        int pr = poll(pfds, nfd, 1000);
        if (pr < 0 && errno != EINTR) break;

        /* 热重载：SIGHUP，或文件 mtime 变化（每 30 秒检查一次） */
        time_t now = time(NULL);
        if (g_reload || now - last_check >= RELOAD_CHECK_SEC) {
            last_check = now;
            if (g_reload) { g_reload = 0; if (reload_upstreams()) fprintf(stderr, "dnsfwd: 收到 SIGHUP，已重载上游 %d 个\n", nup); }
            else if (stat(g_conf, &st) == 0 && st.st_mtime != last_mtime) {
                last_mtime = st.st_mtime;
                if (reload_upstreams()) fprintf(stderr, "dnsfwd: 上游文件变更，已自动重载 %d 个\n", nup);
            }
        }
        if (pr <= 0) continue;

        /* 收包后交给工作池：主线程只做收包与热重载，转发互不阻塞 */
        for (int k = 0; k < nfd; k++) {
            if (!(pfds[k].revents & POLLIN)) continue;
            struct qjob jb;
            jb.fd = fds[k];
            jb.clilen = sizeof jb.cli;
            int n = recvfrom(fds[k], jb.q, sizeof jb.q, 0, (struct sockaddr *)&jb.cli, &jb.clilen);
            if (n <= 0) continue;
            jb.qlen = n;
            pthread_mutex_lock(&g_qmu);
            if (g_qn >= JOBSZ) {
                pthread_mutex_unlock(&g_qmu);
                fprintf(stderr, "dnsfwd: 队列已满（%d），丢弃本次查询\n", JOBSZ);
                continue;
            }
            g_q[g_qt] = jb;
            g_qt = (g_qt + 1) % JOBSZ;
            g_qn++;
            pthread_cond_signal(&g_qcv);
            pthread_mutex_unlock(&g_qmu);
        }
    }
    /* 退出：唤醒并回收工作线程 */
    pthread_mutex_lock(&g_qmu);
    pthread_cond_broadcast(&g_qcv);
    pthread_mutex_unlock(&g_qmu);
    for (int i = 0; i < g_workers; i++) pthread_join(g_wth[i], NULL);
    for (int i = 0; i < nfd; i++) close(fds[i]);
    fprintf(stderr, "dnsfwd: 退出\n");
    return 0;
}
