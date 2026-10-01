#!/bin/sh
# 构建 dnsfwd（aarch64 静态可执行，Android 直接可跑）
#
# 前置：
#   1) aarch64 交叉编译器：apt install gcc-aarch64-linux-gnu binutils-aarch64-linux-gnu
#   2) aarch64 glibc sysroot（静态链接需要目标 libc 头与库），默认 /tmp/xa/sysroot
#      · 可用 deb 解包得到：apt-get download libc6-arm64-cross libc6-dev-arm64-cross
#        mkdir -p /tmp/xa/sysroot && cd /tmp/xa/sysroot && dpkg -x ...deb .
#   3) DoH/DoT 需要 mbedTLS：本脚本会在 tools/third_party/ 下自动下载并交叉编译（仅首次）
#      · 用 WITH_DOH=0 可跳过，编译出"只支持明文 DNS"的精简版
#
# 用法：
#   sh build-dnsfwd.sh                 # 默认带 DoH/DoT
#   WITH_DOH=0 sh build-dnsfwd.sh      # 不带加密上游（体积更小、无第三方依赖）
#   SYSROOT=/path/to/rootfs sh build-dnsfwd.sh
# 产物：./dnsfwd  → 拷贝到 magisk/9router-magisk/system/bin/dnsfwd
set -e
cd "$(dirname "$0")"
ROOT="$PWD"
SYSROOT="${SYSROOT:-/tmp/xa/sysroot}"
CC="${CC:-aarch64-linux-gnu-gcc}"
STRIP="${STRIP:-aarch64-linux-gnu-strip}"
AR="${AR:-${CC%gcc}ar}"
WITH_DOH="${WITH_DOH:-1}"
MB_VER="${MB_VER:-mbedtls-3.6.2}"
MB_URL="https://github.com/Mbed-TLS/mbedtls/releases/download/$MB_VER/$MB_VER.tar.bz2"
MB_DIR="$ROOT/third_party/$MB_VER"

if [ -d "$SYSROOT/usr/bin" ]; then PATH="$SYSROOT/usr/bin:$PATH"; export PATH; fi
for d in "$SYSROOT/usr/lib/x86_64-linux-gnu" "$SYSROOT/usr/lib/gcc-cross/aarch64-linux-gnu/15"; do
  [ -d "$d" ] && LD_LIBRARY_PATH="${LD_LIBRARY_PATH:+$LD_LIBRARY_PATH:}$d"
done
export LD_LIBRARY_PATH

command -v "$CC" >/dev/null 2>&1 || { echo "找不到交叉编译器 $CC"; exit 1; }
[ -d "$SYSROOT" ] || { echo "找不到 sysroot: $SYSROOT"; exit 1; }

EXTRA=""
LIBS=""
if [ "$WITH_DOH" = "1" ]; then
  if [ ! -f "$MB_DIR/library/libmbedtls.a" ]; then
    echo "== 准备 mbedTLS（$MB_VER，仅首次）=="
    mkdir -p "$ROOT/third_party"
    cd "$ROOT/third_party"
    [ -f "$MB_VER.tar.bz2" ] || {
      if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$MB_VER.tar.bz2" "$MB_URL";
      else wget -q -O "$MB_VER.tar.bz2" "$MB_URL"; fi
    }
    [ -d "$MB_VER" ] || tar xf "$MB_VER.tar.bz2"
    cd "$MB_VER"
    echo "   交叉编译 mbedTLS（约 1 分钟）…"
    make -j"$(nproc)" lib CC="$CC" AR="$AR" CFLAGS="-O2 -fPIC -static --sysroot=$SYSROOT" >/dev/null 2>&1
    cd "$ROOT"
  fi
  for f in libmbedtls libmbedx509 libmbedcrypto; do
    [ -f "$MB_DIR/library/$f.a" ] || { echo "mbedTLS 静态库缺失：$f.a"; exit 1; }
  done
  EXTRA="-DWITH_DOH -I$MB_DIR/include -I$MB_DIR/library"
  LIBS="$MB_DIR/library/libmbedtls.a $MB_DIR/library/libmbedx509.a $MB_DIR/library/libmbedcrypto.a"
  echo "编译 dnsfwd（静态 + mbedTLS/DoH/DoT，sysroot=$SYSROOT）…"
else
  echo "编译 dnsfwd（静态，仅明文 DNS；WITH_DOH=0）…"
fi

# shellcheck disable=SC2086
"$CC" -O2 -static --sysroot="$SYSROOT" -pthread $EXTRA -o dnsfwd dnsfwd.c $LIBS
"$STRIP" dnsfwd 2>/dev/null || true
ls -lh dnsfwd
file dnsfwd 2>/dev/null | cut -c1-100 || true

# 自动同步进模块目录：避免"编译了新版却忘了拷贝"这种低级但致命的问题（曾发生两次）
MOD_BIN="$ROOT/../module/bin/dnsfwd"
if [ -d "$(dirname "$MOD_BIN")" ]; then
  cp -f dnsfwd "$MOD_BIN" && echo "已同步到模块：$MOD_BIN"
else
  echo "提示：未找到模块目录，请手动拷贝 dnsfwd"
fi
echo "完成。"
