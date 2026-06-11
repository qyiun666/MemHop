#!/bin/bash
# MemHop Binary Crate发布自动化脚本
# 用途: 一键发布memhop-core到GitHub Packages私有registry

set -e  # 遇到错误立即退出

# ==================== 配置区 ====================
GITHUB_USERNAME="qyiun666"
REPO_NAME="memhop-binary-crate"
REGISTRY_NAME="memhop-private"
CRATE_VERSION="0.25.1"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# ==================== 检查前置条件 ====================
echo_info "检查前置条件..."

# 检查gh CLI
if ! command -v gh &> /dev/null; then
    echo_error "GitHub CLI (gh) 未安装"
    echo_info "请执行: brew install gh"
    exit 1
fi

# 检查GitHub登录状态
if ! gh auth status &> /dev/null; then
    echo_error "GitHub未登录"
    echo_info "请执行: gh auth login"
    exit 1
fi

# 检查Cargo
if ! command -v cargo &> /dev/null; then
    echo_error "Cargo未安装"
    exit 1
fi

echo_info "✅ 前置条件检查通过"

# ==================== 获取GitHub Token ====================
echo ""
echo_warn "请输入你的GitHub Personal Access Token:"
echo_info "Token需要以下权限: read:packages, write:packages, repo"
echo_info "生成地址: https://github.com/settings/tokens"
read -s GITHUB_TOKEN
echo ""

if [ -z "$GITHUB_TOKEN" ]; then
    echo_error "Token不能为空"
    exit 1
fi

# ==================== 创建/验证私有仓库 ====================
echo ""
echo_info "检查私有仓库 ${REPO_NAME}..."

if gh repo view ${GITHUB_USERNAME}/${REPO_NAME} &> /dev/null; then
    echo_info "✅ 仓库已存在"
else
    echo_warn "仓库不存在,正在创建..."
    gh repo create ${REPO_NAME} \
        --private \
        --description "MemHop Binary Crate Registry for closed-source distribution" \
        --add-readme
    
    if [ $? -eq 0 ]; then
        echo_info "✅ 仓库创建成功: https://github.com/${GITHUB_USERNAME}/${REPO_NAME}"
    else
        echo_error "仓库创建失败"
        exit 1
    fi
fi

# ==================== 配置Cargo Registry ====================
echo ""
echo_info "配置Cargo Registry..."

CARGO_CONFIG="$HOME/.cargo/config.toml"

# 备份现有配置
if [ -f "$CARGO_CONFIG" ]; then
    cp "$CARGO_CONFIG" "${CARGO_CONFIG}.backup.$(date +%Y%m%d%H%M%S)"
    echo_info "已备份现有配置: ${CARGO_CONFIG}.backup.*"
fi

# 添加或更新registry配置
cat >> "$CARGO_CONFIG" << EOF

# MemHop Private Registry (added by setup script)
[registries.${REGISTRY_NAME}]
index = "sparse+https://npm.pkg.github.com/@${GITHUB_USERNAME}/${REPO_NAME}"
token = "${GITHUB_TOKEN}"
EOF

echo_info "✅ Cargo Registry配置完成: ${CARGO_CONFIG}"

# ==================== 准备memhop-core ====================
echo ""
echo_info "准备memhop-core..."

cd "$(dirname "$0")/../memhop-core"

# 检查目录
if [ ! -f "Cargo.toml" ]; then
    echo_error "memhop-core/Cargo.toml不存在"
    exit 1
fi

# 清理之前的构建
echo_info "清理之前的构建..."
cargo clean

# 编译
echo_info "编译memhop-core..."
cargo build --release

if [ $? -ne 0 ]; then
    echo_error "编译失败"
    exit 1
fi

echo_info "✅ 编译成功"

# ==================== 打包Binary Crate ====================
echo ""
echo_info "打包Binary Crate (不含源码)..."

cargo package --no-verify

if [ $? -ne 0 ]; then
    echo_error "打包失败"
    exit 1
fi

# 检查生成的.crate文件
CRATE_FILE="target/package/memhop-core-${CRATE_VERSION}.crate"
if [ ! -f "$CRATE_FILE" ]; then
    echo_error ".crate文件不存在: ${CRATE_FILE}"
    exit 1
fi

CRATE_SIZE=$(du -h "$CRATE_FILE" | cut -f1)
echo_info "✅ 打包成功: ${CRATE_FILE} (${CRATE_SIZE})"

# 验证.crate不包含src/
echo_info "验证binary crate不包含源码..."
if tar -tf "$CRATE_FILE" | grep -q "memhop-core-.*/src/"; then
    echo_error "❌ .crate文件包含src/目录!这不是binary crate!"
    exit 1
else
    echo_info "✅ 验证通过: .crate文件不包含源码"
fi

# ==================== 发布到GitHub Packages ====================
echo ""
echo_info "发布到GitHub Packages..."

cargo publish --registry ${REGISTRY_NAME} --token ${GITHUB_TOKEN}

if [ $? -ne 0 ]; then
    echo_error "发布失败"
    echo_warn "可能的原因:"
    echo_warn "1. Token权限不足(需要write:packages)"
    echo_warn "2. 版本号已存在(需要升级version)"
    echo_warn "3. 网络连接问题"
    exit 1
fi

echo_info "✅ 发布成功!"
echo_info "查看包: https://github.com/${GITHUB_USERNAME}/${REPO_NAME}/packages"

# ==================== 验证发布 ====================
echo ""
echo_info "验证发布..."

# 清除本地cargo缓存中的memhop-core
cargo cache -a 2>/dev/null || true

# 尝试从registry下载(不实际构建)
echo_info "测试从registry获取..."
cargo search memhop-core --registry ${REGISTRY_NAME} --limit 1

if [ $? -eq 0 ]; then
    echo_info "✅ 验证成功: 可以从registry获取memhop-core"
else
    echo_warn "⚠️ 验证失败(可能是网络延迟,稍后再试)"
fi

# ==================== 生成meowagent集成说明 ====================
echo ""
echo_info "=========================================="
echo_info "🎉 发布完成!"
echo_info "=========================================="
echo ""
echo_info "meowagent集成步骤:"
echo ""
echo_info "1. 在meowagent/Cargo.toml末尾添加:"
echo ""
echo "   [patch.crates-io]"
echo "   memhop-core = { registry = \"${REGISTRY_NAME}\", version = \"${CRATE_VERSION}\" }"
echo ""
echo_info "2. 重新构建meowagent:"
echo ""
echo "   cd /Volumes/zt_hd/projects/meow/meowagent"
echo "   cargo clean"
echo "   cargo build --release"
echo ""
echo_info "3. 验证看不到源码:"
echo ""
echo "   find ~/.cargo/registry/src -name 'memhop-core-*'"
echo "   # 应该没有输出(没有src/目录)"
echo ""
echo_warn "⚠️ 安全提示:"
echo_warn "- 不要将GitHub Token提交到Git仓库"
echo_warn "- 定期轮换Token"
echo_warn "- meowagent开发者只能看到.rlib二进制,无法查看源码"
echo ""
echo_info "详细文档: /Volumes/zt_hd/projects/meow/memhop/docs/GITHUB-PACKAGES-SETUP.md"
echo ""
