#!/bin/bash

# GitHub repo used for release downloads
REPO="ImoLR/FLVXR2"

# 固定版本号（Release 构建时自动填充，留空则获取最新版）
PINNED_VERSION=""

# GitHub 下载加速镜像（前缀式：<镜像>/https://github.com/...），按可靠性排序。
# 直连 GitHub 不通时依次尝试；可用环境变量 GH_PROXY=https://你的镜像/ 指定优先使用的镜像。
GH_MIRRORS="https://ghfast.top/ https://gh-proxy.com/ https://gcode.hostcentral.cc/"

# 默认服务名
SERVICE_NAME="flvx_agent"
SERVER_ADDR=""
SECRET=""

# 检查并安装必要的下载工具
install_download_tools() {
  local need_install=0
  
  if ! command -v curl &> /dev/null; then
    echo "⚠️  未检测到 curl"
    need_install=1
  fi
  
  if ! command -v wget &> /dev/null; then
    echo "⚠️  未检测到 wget"
    need_install=1
  fi
  
  if [ $need_install -eq 0 ]; then
    return 0
  fi
  
  echo "🔧 正在安装缺失的下载工具..."
  
  OS_TYPE=$(uname -s)
  
  if [[ "$OS_TYPE" == "Darwin" ]]; then
    if command -v brew &> /dev/null; then
      brew install curl wget
    else
      echo "❌ 未检测到 Homebrew，请手动安装 curl 和 wget"
      exit 1
    fi
    return 0
  fi
  
  if [ -f /etc/os-release ]; then
    # 在子 shell 中读取，避免 os-release 里的 VERSION 等变量覆盖面板传入的 VERSION
    DISTRO=$(. /etc/os-release && echo "$ID")
  elif [ -f /etc/redhat-release ]; then
    DISTRO="rhel"
  elif [ -f /etc/debian_version ]; then
    DISTRO="debian"
  else
    DISTRO="unknown"
  fi
  
  case $DISTRO in
    ubuntu|debian|kali)
      apt update
      apt install -y curl wget
      ;;
    centos|rhel|fedora|almalinux|rocky)
      if command -v dnf &> /dev/null; then
        dnf install -y curl wget
      elif command -v yum &> /dev/null; then
        yum install -y curl wget
      fi
      ;;
    alpine)
      apk add --no-cache curl wget
      ;;
    arch|manjaro|endeavouros)
      pacman -S --noconfirm curl wget
      ;;
    opensuse*|sles)
      zypper install -y curl wget
      ;;
    void)
      xbps-install -Sy curl wget
      ;;
    gentoo)
      emerge --ask=n net-misc/curl net-misc/wget
      ;;
    *)
      echo "⚠️  未知发行版，请手动安装 curl 和 wget"
      exit 1
      ;;
  esac
  
  echo "✅ 下载工具安装完成"
}

install_download_tools

# 解析命令行参数
while getopts "a:s:n:" opt; do
  case $opt in
    a) SERVER_ADDR="$OPTARG" ;;
    s) SECRET="$OPTARG" ;;
    n) SERVICE_NAME="$OPTARG" ;;
    *) echo "❌ 无效参数"; exit 1 ;;
  esac
done

# 安装目录 (根据 SERVICE_NAME 动态生成)
INSTALL_DIR="/etc/${SERVICE_NAME}"

# 获取系统架构
get_architecture() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        *)
            echo "amd64"  # 默认使用 amd64
            ;;
    esac
}

# 获取最新版本号
resolve_latest_release_tag() {
  local tag
  # 直接使用 GitHub API 获取最新版本号
  tag=$(curl -fsSL --connect-timeout 5 -m 15 "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/' || echo "")
  if [[ -n "$tag" ]]; then
    echo "$tag"
    return 0
  fi
  return 1
}

resolve_version() {
  if [[ -n "${VERSION:-}" ]]; then
    echo "$VERSION"
    return 0
  fi
  if [[ -n "${FLUX_VERSION:-}" ]]; then
    echo "$FLUX_VERSION"
    return 0
  fi
  if [[ -n "${PINNED_VERSION:-}" ]]; then
    echo "$PINNED_VERSION"
    return 0
  fi

  if resolve_latest_release_tag; then
    return 0
  fi

  echo "❌ 无法获取最新版本号。你可以手动指定版本，例如：VERSION=<版本号> ./install.sh" >&2
  return 1
}

# 构建 GitHub Release 下载地址
build_download_url() {
    local ARCH=$(get_architecture)

    echo "https://github.com/${REPO}/releases/download/${RESOLVED_VERSION}/gost-${ARCH}"
}

# 显示下载源信息
show_download_source() {
    local url="$1"
    echo "🌍 正在下载 ${SERVICE_NAME} (${RESOLVED_VERSION}) ..."
}

# ---------- GitHub 下载：直连 + 加速镜像 + sha256 校验 ----------
GH_SOURCES=()
GH_SOURCES_READY=0
GH_EXPECTED_SHA256=""
GH_FETCH_ERROR=""
GH_REJECT_REASON=""

# 规范化镜像前缀，确保以 / 结尾
gh_normalize_prefix() {
  local p="$1"
  [[ -z "$p" ]] && return 0
  [[ "$p" == */ ]] || p="${p}/"
  echo "$p"
}

# 下载源显示名
gh_source_label() {
  if [[ "$1" == "DIRECT" ]]; then
    echo "GitHub 直连"
  else
    local host="${1#*://}"
    echo "镜像 ${host%%/*}"
  fi
}

# 下载源对应的完整地址（镜像为前缀式）
gh_source_url() {
  if [[ "$1" == "DIRECT" ]]; then
    echo "$2"
  else
    echo "${1}${2}"
  fi
}

# 带超时执行 wget（GNU wget 只尝试 1 次；busybox wget 不支持 -t）
gh_wget() {
  local max_time="$1"
  shift
  local extra=()
  if wget --version 2>/dev/null | grep -q 'GNU Wget'; then
    extra=(-t 1)
  fi
  if command -v timeout &> /dev/null; then
    timeout "$max_time" wget "${extra[@]}" "$@"
  else
    wget "${extra[@]}" "$@"
  fi
}

# 快速探测能否直连 GitHub（跟随跳转到实际下载节点），最多约 8 秒
gh_probe_direct() {
  local url="$1" code
  if command -v curl &> /dev/null; then
    code=$(curl -sIL --connect-timeout 5 -m 8 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null)
    [[ "$code" == "200" ]]
    return
  fi
  gh_wget 10 -q --spider -T 8 "$url" &> /dev/null
}

# 确定下载源顺序：GH_PROXY（如设置）→ GitHub 直连（可达时）→ 加速镜像。每次运行只探测一次。
gh_prepare_sources() {
  [[ $GH_SOURCES_READY -eq 1 ]] && return 0
  GH_SOURCES_READY=1
  GH_SOURCES=()
  local proxy m
  proxy=$(gh_normalize_prefix "${GH_PROXY:-}")
  if [[ -n "$proxy" ]]; then
    echo "🌐 优先使用 GH_PROXY 指定的镜像：${proxy}"
    GH_SOURCES+=("$proxy")
  fi
  echo "🌐 正在检测能否直连 GitHub..."
  if gh_probe_direct "$1"; then
    echo "✅ GitHub 可直连"
    GH_SOURCES+=("DIRECT")
  else
    echo "🌐 检测到无法直连 GitHub，使用加速镜像下载"
  fi
  for m in $GH_MIRRORS; do
    m=$(gh_normalize_prefix "$m")
    [[ "$m" == "$proxy" ]] && continue
    GH_SOURCES+=("$m")
  done
}

# 下载单个地址到文件。
# $3: strict = 低于 50KB/s 持续 10 秒即放弃；lenient = 低于 1KB/s 持续 30 秒才放弃
# $4: 1 = 在终端显示进度条
# 返回：0 成功；2 已连上但太慢/中途卡住（宽松模式可重试）；1 其它失败（原因在 GH_FETCH_ERROR）
gh_fetch() {
  local url="$1" out="$2" mode="${3:-strict}" show_progress="${4:-0}"
  local limit=51200 stime=10 wget_max=240 code rc
  if [[ "$mode" == "lenient" ]]; then
    limit=1024
    stime=30
    wget_max=900
  fi
  GH_FETCH_ERROR=""
  rm -f "$out"
  if command -v curl &> /dev/null; then
    if [[ "$show_progress" == "1" && -t 2 ]]; then
      code=$(curl -fL -# --connect-timeout 5 --speed-limit "$limit" --speed-time "$stime" -m 900 \
        -w '%{http_code}' -o "$out" "$url")
    else
      code=$(curl -fsL --connect-timeout 5 --speed-limit "$limit" --speed-time "$stime" -m 900 \
        -w '%{http_code}' -o "$out" "$url" 2>/dev/null)
    fi
    rc=$?
    [[ $rc -eq 0 ]] && return 0
    case $rc in
      6) GH_FETCH_ERROR="无法解析域名" ;;
      7) GH_FETCH_ERROR="无法连接" ;;
      22) GH_FETCH_ERROR="HTTP ${code}" ;;
      28) GH_FETCH_ERROR="超时或速度过慢" ;;
      35|60) GH_FETCH_ERROR="TLS 握手失败" ;;
      *) GH_FETCH_ERROR="curl 错误码 ${rc}" ;;
    esac
    rm -f "$out"
    if [[ $rc -eq 28 && -n "$code" && "$code" != "000" ]]; then
      return 2
    fi
    return 1
  fi

  gh_wget "$wget_max" -q -T 15 -O "$out" "$url" 2>/dev/null
  rc=$?
  [[ $rc -eq 0 ]] && return 0
  rm -f "$out"
  if [[ $rc -eq 124 ]]; then
    GH_FETCH_ERROR="超时或速度过慢"
    return 2
  fi
  GH_FETCH_ERROR="wget 错误码 ${rc}"
  return 1
}

# 检查下载内容：非空、不是网页；bin 必须是 ELF 可执行文件，script 必须是本安装脚本
gh_validate() {
  local f="$1" kind="$2"
  GH_REJECT_REASON=""
  if [[ ! -s "$f" ]]; then
    GH_REJECT_REASON="文件为空"
    return 1
  fi
  if head -c 512 "$f" | LC_ALL=C grep -qiE '<!doctype|<html'; then
    GH_REJECT_REASON="返回的是网页而不是文件"
    return 1
  fi
  case "$kind" in
    bin)
      if [[ "$(head -c 4 "$f" | tail -c 3)" != "ELF" ]]; then
        GH_REJECT_REASON="不是有效的可执行文件"
        return 1
      fi
      ;;
    script)
      if [[ "$(head -c 2 "$f")" != "#!" ]] || ! grep -q 'install_service' "$f"; then
        GH_REJECT_REASON="不是有效的安装脚本"
        return 1
      fi
      ;;
  esac
  return 0
}

# 计算文件 sha256（无可用工具时输出为空）
gh_sha256_of() {
  if command -v sha256sum &> /dev/null; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum &> /dev/null; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl &> /dev/null; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  fi
}

# 通过同一下载源链获取期望的 sha256（校验文件格式："<hash>  <文件名>"）
gh_fetch_expected_sha256() {
  local sum_url="$1" tmp="$2" src hash
  GH_EXPECTED_SHA256=""
  for src in "${GH_SOURCES[@]}"; do
    if gh_fetch "$(gh_source_url "$src" "$sum_url")" "$tmp" strict; then
      hash=$(head -c 200 "$tmp" | awk 'NR==1{print $1}' | tr 'A-F' 'a-f')
      rm -f "$tmp"
      if [[ "$hash" =~ ^[0-9a-f]{64}$ ]]; then
        GH_EXPECTED_SHA256="$hash"
        echo "🔐 已获取校验值（$(gh_source_label "$src")）"
        return 0
      fi
      echo "⚠️  $(gh_source_label "$src") 返回的校验文件无效，换下一个下载源"
    fi
  done
  rm -f "$tmp"
  return 1
}

# gh_download <GitHub 地址> <输出文件> <bin|script> [校验文件地址]
# 依次尝试各下载源；给出校验文件地址时校验 sha256，校验不一致的文件绝不采用。
gh_download() {
  local url="$1" out="$2" kind="$3" sum_url="${4:-}"
  local part="${out}.part" src mode rc actual
  local sources=() slow_sources=()

  gh_prepare_sources "$url"

  GH_EXPECTED_SHA256=""
  if [[ -n "$sum_url" ]]; then
    echo "🔐 正在获取校验文件..."
    if ! gh_fetch_expected_sha256 "$sum_url" "${out}.sha256.part"; then
      echo "⚠️  无法获取校验文件，跳过 sha256 校验（仅检查文件格式）"
    fi
  fi

  sources=("${GH_SOURCES[@]}")
  for mode in strict lenient; do
    if [[ "$mode" == "lenient" ]]; then
      [[ ${#slow_sources[@]} -eq 0 ]] && break
      echo "⚠️  下载源速度都过慢，放宽速度限制后重试..."
      sources=("${slow_sources[@]}")
    fi
    for src in "${sources[@]}"; do
      echo "⬇️  尝试下载源：$(gh_source_label "$src")"
      gh_fetch "$(gh_source_url "$src" "$url")" "$part" "$mode" 1
      rc=$?
      if [[ $rc -ne 0 ]]; then
        [[ $rc -eq 2 && "$mode" == "strict" ]] && slow_sources+=("$src")
        echo "   ❌ 下载失败（${GH_FETCH_ERROR}）"
        continue
      fi
      if ! gh_validate "$part" "$kind"; then
        rm -f "$part"
        echo "   ❌ 内容无效（${GH_REJECT_REASON}），换下一个下载源"
        continue
      fi
      if [[ -n "$GH_EXPECTED_SHA256" ]]; then
        actual=$(gh_sha256_of "$part")
        if [[ -z "$actual" ]]; then
          echo "   ⚠️  系统缺少 sha256 工具，跳过校验"
        elif [[ "$actual" != "$GH_EXPECTED_SHA256" ]]; then
          rm -f "$part"
          echo "   ❌ sha256 校验失败（期望 ${GH_EXPECTED_SHA256:0:12}…，实际 ${actual:0:12}…），换下一个下载源"
          continue
        else
          echo "   ✅ sha256 校验通过"
        fi
      fi
      mv -f "$part" "$out"
      return 0
    done
  done
  rm -f "$part"
  return 1
}

# 解析版本并构建下载地址
RESOLVED_VERSION=$(resolve_version) || exit 1
DOWNLOAD_HOST="https://github.com/${REPO}/releases/download/${RESOLVED_VERSION}"
DOWNLOAD_URL="$(build_download_url)"

# 显示菜单
show_menu() {
  echo "==============================================="
  echo "              管理脚本"
  echo "==============================================="
  echo "请选择操作："
  echo "1. 安装"
  echo "2. 更新"  
  echo "3. 卸载"
  echo "4. 退出"
  echo "==============================================="
}

# 检查并安装 tcpkill
check_and_install_tcpkill() {
  if command -v tcpkill &> /dev/null; then
    return 0
  fi

  # tcpkill 为可选组件（删除/暂停转发时用于断开该端口上的已有连接），安装失败不影响节点运行
  echo "🔧 安装 tcpkill (dsniff)…（可选组件，最多约 5 分钟，失败会跳过）"

  # 包管理器调用加超时，避免软件源缓慢或不可达时长时间卡住
  local t_update="" t_install="" rc=0
  if command -v timeout &> /dev/null; then
    t_update="timeout 120"
    t_install="timeout 180"
  fi

  OS_TYPE=$(uname -s)
  if [[ "$OS_TYPE" == "Darwin" ]]; then
    if command -v brew &> /dev/null; then
      $t_install brew install dsniff &> /dev/null
    fi
    return 0
  fi

  if [ -f /etc/os-release ]; then
    # 在子 shell 中读取，避免 os-release 里的 VERSION 等变量覆盖面板传入的 VERSION
    DISTRO=$(. /etc/os-release && echo "$ID")
  elif [ -f /etc/redhat-release ]; then
    DISTRO="rhel"
  elif [ -f /etc/debian_version ]; then
    DISTRO="debian"
  else
    echo "⚠️  无法识别系统发行版，跳过 tcpkill 安装"
    return 0
  fi

  case $DISTRO in
    ubuntu|debian)
      echo "   apt 更新软件源..."
      DEBIAN_FRONTEND=noninteractive $t_update apt-get update &> /dev/null
      echo "   apt 安装 dsniff..."
      DEBIAN_FRONTEND=noninteractive $t_install apt-get install -y dsniff &> /dev/null
      rc=$?
      ;;
    centos|rhel|fedora)
      if command -v dnf &> /dev/null; then
        $t_install dnf install -y dsniff &> /dev/null
        rc=$?
      elif command -v yum &> /dev/null; then
        $t_install yum install -y dsniff &> /dev/null
        rc=$?
      fi
      ;;
    alpine)
      $t_install apk add --no-cache dsniff &> /dev/null
      rc=$?
      ;;
    arch|manjaro)
      $t_install pacman -S --noconfirm dsniff &> /dev/null
      rc=$?
      ;;
    opensuse*|sles)
      $t_install zypper install -y dsniff &> /dev/null
      rc=$?
      ;;
    gentoo)
      $t_install emerge --ask=n net-analyzer/dsniff &> /dev/null
      rc=$?
      ;;
    void)
      $t_install xbps-install -Sy dsniff &> /dev/null
      rc=$?
      ;;
  esac

  if command -v tcpkill &> /dev/null; then
    echo "✅ tcpkill 已安装"
  elif [[ $rc -eq 124 ]]; then
    echo "⚠️  tcpkill 安装超时，已跳过（不影响节点运行，可稍后手动安装 dsniff）"
    if [[ "$DISTRO" == "ubuntu" || "$DISTRO" == "debian" ]]; then
      echo "   如之后 apt 提示 dpkg 被中断，请执行：dpkg --configure -a"
    fi
  else
    echo "⚠️  tcpkill 安装失败，已跳过（不影响节点运行，可稍后手动安装 dsniff）"
  fi

  return 0
}

# 自动检测系统中已安装的实例
detect_installed_instances() {
  INSTALLED_INSTANCES=()
  # 扫描 systemd 中带有 Proxy Service 描述的服务
  for svc_file in /etc/systemd/system/*.service; do
    if [[ -f "$svc_file" ]] && grep -q "Proxy Service" "$svc_file" 2>/dev/null; then
      svc_name=$(basename "$svc_file" .service)
      INSTALLED_INSTANCES+=("$svc_name")
    fi
  done
}

# 智能选择实例 (用于更新和卸载)
select_instance() {
  detect_installed_instances
  
  if [[ ${#INSTALLED_INSTANCES[@]} -eq 0 ]]; then
    echo "❌ 未检测到任何已安装的服务实例。"
    return 1
  elif [[ ${#INSTALLED_INSTANCES[@]} -eq 1 ]]; then
    SERVICE_NAME="${INSTALLED_INSTANCES[0]}"
    INSTALL_DIR="/etc/${SERVICE_NAME}"
    echo "🔍 自动选中唯一实例: ${SERVICE_NAME}"
    return 0
  else
    echo "🔍 检测到多个实例，请选择要操作的实例："
    local i=1
    for svc in "${INSTALLED_INSTANCES[@]}"; do
      echo "  $i. $svc"
      ((i++))
    done
    
    while true; do
      read -p "请输入数字选项 (1-${#INSTALLED_INSTANCES[@]}): " choice
      if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -le ${#INSTALLED_INSTANCES[@]} ]; then
        SERVICE_NAME="${INSTALLED_INSTANCES[$((choice-1))]}"
        INSTALL_DIR="/etc/${SERVICE_NAME}"
        echo "✅ 已选择实例: ${SERVICE_NAME}"
        return 0
      else
        echo "❌ 无效选项，请重新输入"
      fi
    done
  fi
}

# 获取用户输入的配置参数 (安装时用)
get_config_params() {
  if [[ -z "$SERVER_ADDR" || -z "$SECRET" ]]; then
    echo "请输入配置参数："
    
    read -p "服务名 (默认: ${SERVICE_NAME}): " input_name
    if [[ -n "$input_name" ]]; then
      SERVICE_NAME="$input_name"
      INSTALL_DIR="/etc/${SERVICE_NAME}"
    fi
    
    if [[ -z "$SERVER_ADDR" ]]; then
      read -p "服务器地址: " SERVER_ADDR
    fi
    
    if [[ -z "$SECRET" ]]; then
      read -p "密钥: " SECRET
    fi
    
    if [[ -z "$SERVER_ADDR" || -z "$SECRET" ]]; then
      echo "❌ 参数不完整，操作取消。"
      exit 1
    fi
  fi
}

# 从旧 flux_agent 迁移配置到新目录
migrate_legacy_config() {
  local old_dir="/etc/flux_agent"
  local old_service="flux_agent"

  # 新目录就是旧目录，无需迁移
  [[ "$INSTALL_DIR" == "$old_dir" ]] && return 0

  # 旧目录不存在，无需迁移
  [[ ! -d "$old_dir" ]] && return 0
  [[ ! -f "$old_dir/config.json" ]] && return 0

  echo "📦 检测到旧的 flux_agent 配置目录：$old_dir"
  echo "📂 新目录：$INSTALL_DIR"

  # 新目录已有配置，询问是否覆盖
  if [[ -f "$INSTALL_DIR/config.json" ]]; then
    echo "⚠️  新目录已存在配置，跳过迁移。"
    return 0
  fi

  echo "是否将旧配置迁移到新目录？"
  read -p "迁移后将停止并禁用旧 flux_agent 服务 [Y/n]: " confirm
  if [[ "$confirm" == "n" || "$confirm" == "N" ]]; then
    echo "⏭️   跳过迁移，将继续在新的空目录安装。"
    return 0
  fi

  echo "🔁  正在迁移配置..."

  # 创建新目录并复制配置
  mkdir -p "$INSTALL_DIR"
  cp "$old_dir/config.json" "$INSTALL_DIR/config.json"
  [[ -f "$old_dir/gost.json" ]] && cp "$old_dir/gost.json" "$INSTALL_DIR/gost.json"

  # 更新 config.json 中的 service_name
  sed -i "s|\"service_name\"[[:space:]]*:[[:space:]]*\"[^\"]*\"|\"service_name\": \"${SERVICE_NAME}\"|" "$INSTALL_DIR/config.json"
  # 同步 node_id（如果旧配置有）
  local old_node_id
  old_node_id=$(grep -o '"node_id"[[:space:]]*:[[:space:]]*[0-9]*' "$old_dir/config.json" | grep -o '[0-9]*')
  if [[ -n "$old_node_id" ]]; then
    # 如果新配置没有 node_id，从旧配置补上
    if ! grep -q '"node_id"' "$INSTALL_DIR/config.json" 2>/dev/null; then
      sed -i "s|^\}$|  \"node_id\": ${old_node_id}\n}|" "$INSTALL_DIR/config.json"
    fi
  fi

  chmod 600 "$INSTALL_DIR"/*.json

  # 停止旧 flux_agent 服务
  if systemctl list-units --full -all | grep -Fq "${old_service}.service"; then
    echo "🛑  停止旧 ${old_service} 服务..."
    systemctl stop ${old_service} 2>/dev/null
    systemctl disable ${old_service} 2>/dev/null
  fi

  # 删除旧 service 文件
  if [[ -f "/etc/systemd/system/${old_service}.service" ]]; then
    rm -f "/etc/systemd/system/${old_service}.service"
    echo "🧹 已删除旧服务文件"
  fi

  # 询问是否删除旧配置目录
  echo "是否删除旧的配置目录（$old_dir）？"
  read -p "建议确认新服务正常运行后再删除 [y/N]: " del_old
  if [[ "$del_old" == "y" || "$del_old" == "Y" ]]; then
    rm -rf "$old_dir"
    echo "🧹 已删除旧配置目录"
  else
    echo "⏭️  保留旧配置目录，如需手动删除请执行：rm -rf $old_dir"
  fi

  systemctl daemon-reload
  echo "✅ 配置迁移完成"
}

# 等待节点连接面板（最多 15 秒）：config.json 写入 node_id，或本次启动后的日志出现 WebSocket 连接成功
wait_panel_connection() {
  local since="$1" deadline
  deadline=$(( $(date +%s) + 15 ))
  echo "⏳ 等待节点连接面板（最多 15 秒）..."
  while :; do
    if grep -Eq '"node_id"[[:space:]]*:[[:space:]]*[1-9][0-9]*' "$INSTALL_DIR/config.json" 2>/dev/null; then
      echo "✅ 节点已连接面板"
      return 0
    fi
    if command -v journalctl &> /dev/null && \
       journalctl -u "${SERVICE_NAME}" --since "$since" --no-pager -q -o cat 2>/dev/null | grep -q 'WebSocket 连接建立成功'; then
      echo "✅ 节点已连接面板"
      return 0
    fi
    [[ $(date +%s) -ge $deadline ]] && break
    sleep 1
  done
  echo "⚠️ 15 秒内未连上面板，请检查面板地址/网络 (journalctl -u ${SERVICE_NAME} -n 50)"
  return 1
}

# 安装功能
install_service() {
  get_config_params
  echo "🚀 开始安装 ${SERVICE_NAME}..."

  check_and_install_tcpkill

  # 如果是新默认名称且检测到旧 flux_agent，执行迁移
  migrate_legacy_config
  
  mkdir -p "$INSTALL_DIR"

  if systemctl list-units --full -all | grep -Fq "${SERVICE_NAME}.service"; then
    echo "🔍 检测到已存在的 ${SERVICE_NAME} 服务"
    systemctl stop ${SERVICE_NAME} 2>/dev/null && echo "🛑 停止服务"
    systemctl disable ${SERVICE_NAME} 2>/dev/null && echo "🚫 禁用自启"
  fi

  [[ -f "$INSTALL_DIR/${SERVICE_NAME}" ]] && echo "🧹 删除旧文件 ${SERVICE_NAME}" && rm -f "$INSTALL_DIR/${SERVICE_NAME}"

  # 显示下载源并下载
  show_download_source "$DOWNLOAD_URL"
  ARCH=$(get_architecture)

  # 依次尝试 GitHub 直连与加速镜像，并校验 sha256
  if ! gh_download "$DOWNLOAD_URL" "$INSTALL_DIR/${SERVICE_NAME}" bin "${DOWNLOAD_URL}.sha256" || \
     [[ ! -s "$INSTALL_DIR/${SERVICE_NAME}" ]]; then
    echo "❌ 无法下载版本 ${RESOLVED_VERSION} 的 ${SERVICE_NAME}，停止安装。"
    echo "   可指定其它加速镜像重试，例如：GH_PROXY=https://你的镜像/ ./install.sh ..."
    exit 1
  fi
  chmod +x "$INSTALL_DIR/${SERVICE_NAME}"
  echo "✅ 下载完成"

  echo "🔎 ${SERVICE_NAME} 版本：$($INSTALL_DIR/${SERVICE_NAME} -V)"

  CONFIG_FILE="$INSTALL_DIR/config.json"
  echo "📄 创建新配置：config.json"
  cat > "$CONFIG_FILE" <<EOF
{
  "addr": "$SERVER_ADDR",
  "secret": "$SECRET",
  "service_name": "$SERVICE_NAME"
}
EOF

  GOST_CONFIG="$INSTALL_DIR/gost.json"
  if [[ -f "$GOST_CONFIG" ]]; then
    echo "⏭️ 跳过配置文件: gost.json (已存在)"
  else
    echo "📄 创建新配置: gost.json"
    cat > "$GOST_CONFIG" <<EOF
{}
EOF
  fi

  chmod 600 "$INSTALL_DIR"/*.json

  SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=${SERVICE_NAME} Proxy Service
After=network.target

[Service]
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/${SERVICE_NAME} -C $INSTALL_DIR/config.json
Restart=on-failure
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable ${SERVICE_NAME}
  # 记录启动时间，只在本次启动之后的日志里判断是否连上面板
  SERVICE_START_TS=$(date '+%Y-%m-%d %H:%M:%S')
  systemctl start ${SERVICE_NAME}

  echo "🔄 检查服务状态..."
  if systemctl is-active --quiet ${SERVICE_NAME}; then
    echo "✅ 服务已启动"

    wait_panel_connection "$SERVICE_START_TS"

    echo "📁 配置目录：$INSTALL_DIR"
    echo "🔧 服务状态：$(systemctl is-active ${SERVICE_NAME})"
  else
    echo "❌ ${SERVICE_NAME} 服务启动失败，请执行以下命令查看状态："
    echo "systemctl status ${SERVICE_NAME} --no-pager"
  fi
}

# 更新功能
update_service() {
  # 智能选择实例
  if ! select_instance; then
    return 1
  fi

  # 从旧的 flux_agent 自动迁移到 flvx_agent
  if [[ "$SERVICE_NAME" == "flux_agent" ]]; then
    echo "📦 检测到旧服务名称: flux_agent，正在迁移到 flvx_agent..."
    local old_dir="/etc/flux_agent"
    local new_name="flvx_agent"
    local new_dir="/etc/${new_name}"

    # 停止旧服务
    systemctl stop flux_agent 2>/dev/null
    systemctl disable flux_agent 2>/dev/null

    # 创建新目录并复制配置
    mkdir -p "$new_dir"
    [[ -f "$old_dir/config.json" ]] && cp "$old_dir/config.json" "$new_dir/config.json"
    [[ -f "$old_dir/gost.json" ]] && cp "$old_dir/gost.json" "$new_dir/gost.json"

    # 更新 config.json 中的 service_name
    sed -i "s|\"service_name\"[[:space:]]*:[[:space:]]*\"[^\"]*\"|\"service_name\": \"${new_name}\"|" "$new_dir/config.json"
    chmod 600 "$new_dir"/*.json

    # 复制现成的 service 文件并改名
    if [[ -f "/etc/systemd/system/flux_agent.service" ]]; then
      cp "/etc/systemd/system/flux_agent.service" "/etc/systemd/system/${new_name}.service"
      sed -i "s/flux_agent/${new_name}/g" "/etc/systemd/system/${new_name}.service"
      sed -i "s|/etc/flux_agent|${new_dir}|g" "/etc/systemd/system/${new_name}.service"
      rm -f "/etc/systemd/system/flux_agent.service"
    fi

    systemctl daemon-reload
    SERVICE_NAME="$new_name"
    INSTALL_DIR="$new_dir"
    echo "✅ 服务名称已迁移到: ${SERVICE_NAME}"
  fi

  echo "🔄 开始更新 ${SERVICE_NAME}..."
  
  SCRIPT_PATH="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || echo "$0")"
  SCRIPT_DOWNLOAD_URL="${DOWNLOAD_HOST}/install.sh"
  
  echo "⬇️ 正在检查并更新安装脚本自身..."
  if gh_download "$SCRIPT_DOWNLOAD_URL" "${SCRIPT_PATH}.new" script && [[ -s "${SCRIPT_PATH}.new" ]]; then
    mv "${SCRIPT_PATH}.new" "$SCRIPT_PATH"
    chmod +x "$SCRIPT_PATH"
    echo "✅ 安装脚本已更新覆盖"
  else
    echo "❌ 无法下载版本 ${RESOLVED_VERSION} 的 install.sh，停止更新"
    rm -f "${SCRIPT_PATH}.new" 2>/dev/null
    return 1
  fi

  echo "📥 使用服务下载地址：$DOWNLOAD_URL"
  
  check_and_install_tcpkill
  
  # 显示下载源并下载
  show_download_source "$DOWNLOAD_URL"
  ARCH=$(get_architecture)

  # 依次尝试 GitHub 直连与加速镜像，并校验 sha256
  if ! gh_download "$DOWNLOAD_URL" "$INSTALL_DIR/${SERVICE_NAME}.new" bin "${DOWNLOAD_URL}.sha256" || \
     [[ ! -s "$INSTALL_DIR/${SERVICE_NAME}.new" ]]; then
    echo "❌ 无法下载版本 ${RESOLVED_VERSION} 的 ${SERVICE_NAME}，停止更新。"
    echo "   可指定其它加速镜像重试，例如：GH_PROXY=https://你的镜像/ ./install.sh"
    rm -f "$INSTALL_DIR/${SERVICE_NAME}.new" 2>/dev/null
    return 1
  fi

  if systemctl list-units --full -all | grep -Fq "${SERVICE_NAME}.service"; then
    echo "🛑 停止 ${SERVICE_NAME} 服务..."
    systemctl stop ${SERVICE_NAME}
  fi

  mv "$INSTALL_DIR/${SERVICE_NAME}.new" "$INSTALL_DIR/${SERVICE_NAME}"
  chmod +x "$INSTALL_DIR/${SERVICE_NAME}"

  # 修复旧版 service 日志丢弃问题
  SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
  if [[ -f "$SERVICE_FILE" ]]; then
    if grep -q "StandardOutput=null" "$SERVICE_FILE" || grep -q "StandardError=null" "$SERVICE_FILE"; then
      echo "🔧 修复 service 日志配置 (null -> journal)..."
      sed -i 's/^StandardOutput=null$/StandardOutput=journal/' "$SERVICE_FILE"
      sed -i 's/^StandardError=null$/StandardError=journal/' "$SERVICE_FILE"
      systemctl daemon-reload
    fi
  fi
  
  echo "🔎 新版本：$($INSTALL_DIR/${SERVICE_NAME} -V)"

  echo "🔄 重启服务..."
  systemctl restart ${SERVICE_NAME}
  
  echo "✅ 更新完成，服务已重新启动。"
}

# 卸载功能
uninstall_service() {
  # 智能选择实例
  if ! select_instance; then
    return 1
  fi

  echo "🗑️ 开始卸载 ${SERVICE_NAME}..."
  
  read -p "确认卸载 ${SERVICE_NAME} 吗？此操作将删除所有相关文件 (y/N): " confirm
  if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "❌ 取消卸载"
    return 0
  fi

  if systemctl list-units --full -all | grep -Fq "${SERVICE_NAME}.service"; then
    echo "🛑 停止并禁用服务..."
    systemctl stop ${SERVICE_NAME} 2>/dev/null
    systemctl disable ${SERVICE_NAME} 2>/dev/null
  fi

  if [[ -f "/etc/systemd/system/${SERVICE_NAME}.service" ]]; then
    rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
    echo "🧹 删除服务文件"
  fi

  if [[ -d "$INSTALL_DIR" ]]; then
    rm -rf "$INSTALL_DIR"
    echo "🧹 删除安装目录: $INSTALL_DIR"
  fi

  systemctl daemon-reload

  echo "✅ 卸载完成"
}

# 主逻辑
main() {
  # 支持直接传参操作 (用于 bash <(curl ...)> uninstall 模式)
  if [[ "$1" == "uninstall" ]]; then
    uninstall_service
    exit $?
  fi

  if [[ -n "$SERVER_ADDR" && -n "$SECRET" ]]; then
    install_service
    exit 0
  fi

  while true; do
    show_menu
    read -p "请输入选项 (1-4): " choice
    
    case $choice in
      1)
        install_service
        exit 0
        ;;
      2)
        update_service
        exit $?
        ;;
      3)
        uninstall_service
        exit 0
        ;;
      4)
        echo "👋 退出脚本"
        exit 0
        ;;
      *)
        echo "❌ 无效选项，请输入 1-4"
        echo ""
        ;;
    esac
  done
}

# 执行主函数
main "$@"
